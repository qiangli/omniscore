package content

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qiangli/omniscore/internal/store"
)

// LoadFromDisk discovers and upserts every test/curve under contentRoot. It
// supports two layouts on the same call:
//
//  1. Flat (legacy SAT demo): contentRoot/tests/*.json + contentRoot/curves/*.json
//  2. Per-test subfolder: contentRoot/<exam>/<slug>/{test.json,curve.json,figures/*.png}
//
// Both branches may apply on a single boot (mixed layouts are fine). Subdirs
// without a recognisable structure (no tests/ child for layout 1, no
// test.json grandchild for layout 2) are silently skipped so stray dirs
// (.git, scratch caches) don't break startup. The function is idempotent.
func LoadFromDisk(ctx context.Context, s *store.Store, contentRoot string) error {
	// Legacy flat layout.
	flatTests := filepath.Join(contentRoot, "tests")
	if info, err := os.Stat(flatTests); err == nil && info.IsDir() {
		if err := loadFlat(ctx, s, flatTests, filepath.Join(contentRoot, "curves")); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", flatTests, err)
	}

	// Per-test subfolder layout.
	entries, err := os.ReadDir(contentRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read content root %s: %w", contentRoot, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "tests" || name == "curves" || strings.HasPrefix(name, ".") {
			continue
		}
		examDir := filepath.Join(contentRoot, name)
		if err := loadExam(ctx, s, examDir, name); err != nil {
			return err
		}
	}
	return nil
}

// loadExam walks one <exam> directory looking for <slug>/test.json files. Any
// subdir without a test.json is skipped (e.g. a stale figures/ from the old
// layout, or a workdir cache).
func loadExam(ctx context.Context, s *store.Store, examDir, examPrefix string) error {
	entries, err := os.ReadDir(examDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read exam dir %s: %w", examDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		slugDir := filepath.Join(examDir, e.Name())
		testPath := filepath.Join(slugDir, "test.json")
		if info, err := os.Stat(testPath); err != nil || info.IsDir() {
			continue
		}
		if err := loadSubfolder(ctx, s, slugDir, examPrefix); err != nil {
			return err
		}
	}
	return nil
}

// loadFlat handles the legacy flat layout: every <slug>.json under testsDir
// is treated as one Test, with an optional sibling <slug>.json under
// curvesDir for the scoring curve. No figure src rewriting.
func loadFlat(ctx context.Context, s *store.Store, testsDir, curvesDir string) error {
	entries, err := os.ReadDir(testsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read tests dir: %w", err)
	}
	now := time.Now().UnixMilli()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(testsDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var t Test
		if err := json.Unmarshal(raw, &t); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if t.Slug == "" {
			return fmt.Errorf("%s: missing slug", path)
		}
		if err := upsertTest(ctx, s, t, raw, now); err != nil {
			return err
		}
		curvePath := filepath.Join(curvesDir, t.Slug+".json")
		if err := loadCurve(ctx, s, curvePath, t.Slug); err != nil {
			return err
		}
	}
	return nil
}

// loadSubfolder handles one <slug>/ directory under <contentRoot>/<exam>/.
// Reads test.json (required), curve.json (optional), and rewrites figure
// srcs to absolute /api/figures/<exam>/<slug>/<file> URLs.
func loadSubfolder(ctx context.Context, s *store.Store, slugDir, examPrefix string) error {
	testPath := filepath.Join(slugDir, "test.json")
	raw, err := os.ReadFile(testPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", testPath, err)
	}
	var t Test
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("parse %s: %w", testPath, err)
	}
	if t.Slug == "" {
		return fmt.Errorf("%s: missing slug", testPath)
	}
	rewriteFigureSrcs(&t, examPrefix)
	rewritten, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("re-marshal %s: %w", testPath, err)
	}
	if err := upsertTest(ctx, s, t, rewritten, time.Now().UnixMilli()); err != nil {
		return err
	}
	return loadCurve(ctx, s, filepath.Join(slugDir, "curve.json"), t.Slug)
}

func upsertTest(ctx context.Context, s *store.Store, t Test, blob []byte, publishedAt int64) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO test_templates(slug, title, exam_type, json_blob, published_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			title=excluded.title,
			exam_type=excluded.exam_type,
			json_blob=excluded.json_blob,
			published_at=excluded.published_at`,
		t.Slug, t.Title, t.ExamType, string(blob), publishedAt,
	)
	return err
}

func loadCurve(ctx context.Context, s *store.Store, curvePath, slug string) error {
	curveRaw, err := os.ReadFile(curvePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read curve %s: %w", curvePath, err)
	}
	var c Curve
	if err := json.Unmarshal(curveRaw, &c); err != nil {
		return fmt.Errorf("parse curve %s: %w", curvePath, err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM test_scoring_curves WHERE test_slug = ?`, slug); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for section, points := range c.Sections {
		for _, p := range points {
			low, high := p.ScaledLow, p.ScaledHigh
			scaled := p.Scaled
			// Range given but no explicit midpoint: derive it.
			if scaled == 0 && (low != 0 || high != 0) {
				scaled = (low + high) / 2
			}
			// Midpoint only: leave low/high NULL (legacy AP + demo SAT shape).
			var lowArg, highArg any
			if low != 0 || high != 0 {
				lowArg, highArg = low, high
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO test_scoring_curves(test_slug, section, raw_score, scaled_score, scaled_score_low, scaled_score_high) VALUES (?,?,?,?,?,?)`,
				slug, section, p.Raw, scaled, lowArg, highArg,
			); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit()
}

// rewriteFigureSrcs walks every Figure in t and rewrites Src to an absolute
// /api/figures/<examPrefix>/<slug>/<basename> URL. Idempotent on already-rewritten srcs.
func rewriteFigureSrcs(t *Test, examPrefix string) {
	rewrite := func(f *Figure) {
		if f == nil || f.Src == "" {
			return
		}
		if strings.HasPrefix(f.Src, "/api/figures/") {
			return // already absolute
		}
		f.Src = "/api/figures/" + examPrefix + "/" + t.Slug + "/" + filepath.Base(f.Src)
	}
	for i := range t.Modules {
		for j := range t.Modules[i].Questions {
			q := &t.Modules[i].Questions[j]
			rewrite(q.PassageFigure)
			rewrite(q.StemFigure)
			for k := range q.Choices {
				rewrite(q.Choices[k].Figure)
			}
		}
	}
}

// Get retrieves a published Test by slug.
func Get(ctx context.Context, s *store.Store, slug string) (Test, error) {
	var blob string
	err := s.DB.QueryRowContext(ctx, `SELECT json_blob FROM test_templates WHERE slug = ?`, slug).Scan(&blob)
	if err != nil {
		return Test{}, err
	}
	var t Test
	if err := json.Unmarshal([]byte(blob), &t); err != nil {
		return Test{}, err
	}
	return t, nil
}

// List returns a metadata-only listing of every published test.
type Listing struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	ExamType string `json:"exam_type"`
	Subject  string `json:"subject,omitempty"`
	Modules  int    `json:"modules"`
}

func List(ctx context.Context, s *store.Store) ([]Listing, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT slug, title, exam_type, json_blob FROM test_templates ORDER BY title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Listing
	for rows.Next() {
		var l Listing
		var blob string
		if err := rows.Scan(&l.Slug, &l.Title, &l.ExamType, &blob); err != nil {
			return nil, err
		}
		var t Test
		if err := json.Unmarshal([]byte(blob), &t); err != nil {
			return nil, err
		}
		l.Subject = t.Subject
		l.Modules = len(t.Modules)
		out = append(out, l)
	}
	return out, rows.Err()
}

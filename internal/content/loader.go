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
//  1. Flat (legacy SAT): contentRoot/tests/*.json + contentRoot/curves/*.json
//  2. Per-exam-type: contentRoot/<exam>/tests/*.json + contentRoot/<exam>/curves/*.json
//     plus contentRoot/<exam>/figures/<slug>/*.png served by the static handler.
//
// Both branches may apply on a single boot (mixed layouts are fine). Subdirs
// without a tests/ child are silently skipped so stray dirs (.git, figures,
// etc.) don't break startup. The function is idempotent.
func LoadFromDisk(ctx context.Context, s *store.Store, contentRoot string) error {
	// Legacy flat layout.
	flatTests := filepath.Join(contentRoot, "tests")
	if info, err := os.Stat(flatTests); err == nil && info.IsDir() {
		if err := loadOne(ctx, s, flatTests, filepath.Join(contentRoot, "curves"), ""); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", flatTests, err)
	}

	// Per-exam-type subdirs.
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
		examTests := filepath.Join(examDir, "tests")
		if info, err := os.Stat(examTests); err != nil || !info.IsDir() {
			continue // not an exam-type subdir; ignore (e.g. figures/, scratch dirs)
		}
		if err := loadOne(ctx, s, examTests, filepath.Join(examDir, "curves"), name); err != nil {
			return err
		}
	}
	return nil
}

// loadOne walks one tests/+curves/ pair, upserting tests and (re)inserting
// curve points. examPrefix, when non-empty, is the per-exam-type subdir name
// (e.g. "ap") and triggers figure-src URL rewriting before the JSON blob is
// persisted.
func loadOne(ctx context.Context, s *store.Store, testsDir, curvesDir, examPrefix string) error {
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

		blob := raw
		if examPrefix != "" {
			rewriteFigureSrcs(&t, examPrefix)
			rewritten, err := json.Marshal(t)
			if err != nil {
				return fmt.Errorf("re-marshal %s: %w", path, err)
			}
			blob = rewritten
		}

		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO test_templates(slug, title, exam_type, json_blob, published_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(slug) DO UPDATE SET
				title=excluded.title,
				exam_type=excluded.exam_type,
				json_blob=excluded.json_blob,
				published_at=excluded.published_at`,
			t.Slug, t.Title, t.ExamType, string(blob), now,
		); err != nil {
			return err
		}

		// Optional matching curve in curvesDir/<slug>.json
		curvePath := filepath.Join(curvesDir, t.Slug+".json")
		curveRaw, err := os.ReadFile(curvePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read curve %s: %w", curvePath, err)
		}
		var c Curve
		if err := json.Unmarshal(curveRaw, &c); err != nil {
			return fmt.Errorf("parse curve %s: %w", curvePath, err)
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM test_scoring_curves WHERE test_slug = ?`, t.Slug); err != nil {
			return err
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for section, points := range c.Sections {
			for _, p := range points {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO test_scoring_curves(test_slug, section, raw_score, scaled_score) VALUES (?,?,?,?)`,
					t.Slug, section, p.Raw, p.Scaled,
				); err != nil {
					_ = tx.Rollback()
					return err
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
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

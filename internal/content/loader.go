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

// LoadFromDisk reads every *.json file under testsDir (and matching curve under
// curvesDir if present), upserting both into the database. It is idempotent
// and safe to call on every server start.
func LoadFromDisk(ctx context.Context, s *store.Store, testsDir, curvesDir string) error {
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
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO test_templates(slug, title, exam_type, json_blob, published_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(slug) DO UPDATE SET
				title=excluded.title,
				exam_type=excluded.exam_type,
				json_blob=excluded.json_blob,
				published_at=excluded.published_at`,
			t.Slug, t.Title, t.ExamType, string(raw), now,
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
		l.Modules = len(t.Modules)
		out = append(out, l)
	}
	return out, rows.Err()
}

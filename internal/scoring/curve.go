// Package scoring converts raw correct-answer counts to scaled scores.
package scoring

import (
	"context"
	"database/sql"

	"github.com/qiangli/omniscore/internal/store"
)

// Section is "rw" or "math" for SAT (future: subject codes for AP).
type Section string

// Scale returns the scaled score for a given (test, section, raw) triple.
// If raw exceeds the curve's max it returns the highest scaled score; if it
// undershoots the min, the lowest. Missing curve returns ok=false.
func Scale(ctx context.Context, s *store.Store, testSlug string, section Section, raw int) (scaled int, ok bool, err error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT scaled_score FROM test_scoring_curves
		WHERE test_slug = ? AND section = ? AND raw_score = ?`,
		testSlug, section, raw,
	)
	err = row.Scan(&scaled)
	if err == nil {
		return scaled, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	// Fall back to nearest neighbour (clamped).
	row = s.DB.QueryRowContext(ctx, `
		SELECT raw_score, scaled_score FROM test_scoring_curves
		WHERE test_slug = ? AND section = ?
		ORDER BY ABS(raw_score - ?) ASC LIMIT 1`,
		testSlug, section, raw,
	)
	var nearestRaw int
	if err := row.Scan(&nearestRaw, &scaled); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	return scaled, true, nil
}

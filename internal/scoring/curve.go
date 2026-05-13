// Package scoring converts raw correct-answer counts to scaled scores.
package scoring

import (
	"context"
	"database/sql"

	"github.com/qiangli/omniscore/internal/store"
)

// Section is a free-form section id matching content.Module.Section
// ("rw"/"math" for SAT, AP subject codes, future exams, …).
type Section string

// Scale returns the scaled score for a given (test, section, raw) triple.
// For curves that carry a [low, high] band (SAT), this returns the midpoint
// stored in scaled_score. Use ScaleRange when you want both bounds.
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

// ScaleRange returns the [low, high] scaled band for SAT-style curves,
// matching the printed College Board scoring guides. For curves that only
// store a midpoint (AP, the legacy SAT demo) low == high == midpoint, so
// callers can always render `low..high` safely.
// Missing curve returns ok=false.
func ScaleRange(ctx context.Context, s *store.Store, testSlug string, section Section, raw int) (low, high int, ok bool, err error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT scaled_score, scaled_score_low, scaled_score_high FROM test_scoring_curves
		WHERE test_slug = ? AND section = ? AND raw_score = ?`,
		testSlug, section, raw,
	)
	var mid int
	var nullLow, nullHigh sql.NullInt64
	err = row.Scan(&mid, &nullLow, &nullHigh)
	if err == nil {
		return resolveRange(mid, nullLow, nullHigh), resolveRangeHigh(mid, nullLow, nullHigh), true, nil
	}
	if err != sql.ErrNoRows {
		return 0, 0, false, err
	}
	row = s.DB.QueryRowContext(ctx, `
		SELECT scaled_score, scaled_score_low, scaled_score_high FROM test_scoring_curves
		WHERE test_slug = ? AND section = ?
		ORDER BY ABS(raw_score - ?) ASC LIMIT 1`,
		testSlug, section, raw,
	)
	if err := row.Scan(&mid, &nullLow, &nullHigh); err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	return resolveRange(mid, nullLow, nullHigh), resolveRangeHigh(mid, nullLow, nullHigh), true, nil
}

func resolveRange(mid int, low, _ sql.NullInt64) int {
	if low.Valid {
		return int(low.Int64)
	}
	return mid
}

func resolveRangeHigh(mid int, _, high sql.NullInt64) int {
	if high.Valid {
		return int(high.Int64)
	}
	return mid
}

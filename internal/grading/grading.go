// Package grading decides whether a student's submitted answer matches the
// question's answer key. Each Question.Type registers a Grader; new question
// types (multi-select, matching, essay, …) plug in by calling Register()
// from an init() so the rest of the server stays unaware.
//
// The grader contract intentionally takes one string from the wire (whatever
// the frontend posted as `choice`) and returns a single bool. Equivalence
// classes — "0.5" == "1/2" == ".5" for SAT SPR — are baked into each
// grader's normalization.
package grading

import (
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/qiangli/omniscore/internal/content"
)

// Grader is the contract every question type must satisfy. Implementations
// must be pure (no I/O, no global state mutation) and safe for concurrent use.
type Grader interface {
	// IsCorrect returns true if submitted matches q's answer key under the
	// grader's equivalence rules. Empty submissions always return false.
	IsCorrect(q content.Question, submitted string) bool
	// CorrectDisplay returns the canonical answer to display on the results
	// screen ("C" for MCQ, "0.5" or "0.5 or 1/2" for SPR).
	CorrectDisplay(q content.Question) string
}

var registry = map[string]Grader{
	"":                      mcqGrader{},
	content.QuestionTypeMCQ: mcqGrader{},
	content.QuestionTypeSPR: sprGrader{},
}

// Register installs a Grader for a Question.Type. Calling Register from a
// package init() makes new question types available transparently to the
// session layer.
func Register(typ string, g Grader) { registry[typ] = g }

// IsCorrect dispatches to the grader for q.Type. Unknown types fail closed
// (return false) rather than silently treating any answer as correct.
func IsCorrect(q content.Question, submitted string) bool {
	if strings.TrimSpace(submitted) == "" {
		return false
	}
	g, ok := registry[q.Type]
	if !ok {
		return false
	}
	return g.IsCorrect(q, submitted)
}

// CorrectDisplay returns the canonical correct-answer string for the
// results screen. Unknown types return "" so callers fall back gracefully.
func CorrectDisplay(q content.Question) string {
	g, ok := registry[q.Type]
	if !ok {
		return ""
	}
	return g.CorrectDisplay(q)
}

// -- MCQ -----------------------------------------------------------------

type mcqGrader struct{}

func (mcqGrader) IsCorrect(q content.Question, submitted string) bool {
	return strings.TrimSpace(submitted) == q.AnswerLabel
}

func (mcqGrader) CorrectDisplay(q content.Question) string { return q.AnswerLabel }

// -- SPR (SAT Math Student-Produced Response) -----------------------------
//
// SAT SPR grading rules (from College Board's Digital SAT specification):
//   - The student types into a single box. Allowed characters: digits 0-9,
//     decimal point, fraction slash, negative sign. (Comma and space are
//     accepted by the real test as ignored.)
//   - Equivalent forms accept: integer / decimal / fraction / mixed-number-
//     like "7/4" (not "1 3/4" — Bluebook rejects mixed numbers). E.g.
//     "0.5" == "1/2" == ".5" == "0.50".
//   - For values with a non-terminating decimal, at least 3 significant
//     digits past the decimal are required. We don't enforce that here; we
//     accept anything that mathematically matches one of AnswerValues.
//   - Some questions accept multiple answers (e.g. solutions of a quadratic
//     "2 or -12"). Each AnswerValues entry is one accepted answer; a
//     submission matches the question if it equals any one of them.
//   - Ordered pairs like "(3,5)" don't appear on real SAT SPR but we accept
//     "a; b" / "a, b" pairs so future tests can use them — see splitParts.

type sprGrader struct{}

func (g sprGrader) IsCorrect(q content.Question, submitted string) bool {
	sub := normalizeSPR(submitted)
	if sub == nil {
		return false
	}
	for _, v := range q.AnswerValues {
		key := normalizeSPR(v)
		if key == nil {
			continue
		}
		if sprPartsEqual(sub, key) {
			return true
		}
	}
	return false
}

func (g sprGrader) CorrectDisplay(q content.Question) string {
	if len(q.AnswerValues) == 0 {
		return ""
	}
	if len(q.AnswerValues) == 1 {
		return q.AnswerValues[0]
	}
	return strings.Join(q.AnswerValues, " or ")
}

// normalizeSPR returns a slice of canonical part rationals, or nil for
// unparseable input. Multi-part answers ("2; -12", "2, -12") are split into
// individual parts; the comparison is order-insensitive.
func normalizeSPR(s string) []*big.Rat {
	parts := splitParts(s)
	if len(parts) == 0 {
		return nil
	}
	out := make([]*big.Rat, 0, len(parts))
	for _, p := range parts {
		r, ok := parseRat(p)
		if !ok {
			return nil
		}
		out = append(out, r)
	}
	return out
}

// sprPartsEqual compares two part slices order-insensitively (real Digital
// SAT lets a student type "-12; 2" or "2; -12" for the same multi-solution
// question).
func sprPartsEqual(a, b []*big.Rat) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]*big.Rat(nil), a...)
	bs := append([]*big.Rat(nil), b...)
	sortRats(as)
	sortRats(bs)
	for i := range as {
		if as[i].Cmp(bs[i]) != 0 {
			return false
		}
	}
	return true
}

func sortRats(rs []*big.Rat) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Cmp(rs[j]) < 0 })
}

var partSplitter = regexp.MustCompile(`[;,]`)

func splitParts(s string) []string {
	raw := partSplitter.Split(s, -1)
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// parseRat parses an integer, decimal, or fraction into a *big.Rat.
// Examples accepted: "2", "-12", "0.5", ".5", "1/2", "-3/4", "100", "+7".
// Trailing % is treated as /100 (SAT SPR doesn't use percents, but harmless).
// Returns (nil, false) on anything that doesn't look like a number.
func parseRat(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	pct := false
	if strings.HasSuffix(s, "%") {
		pct = true
		s = strings.TrimSuffix(s, "%")
	}
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return nil, false
	}
	if pct {
		r.Quo(r, big.NewRat(100, 1))
	}
	return r, true
}

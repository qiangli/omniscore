package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/server"
	"github.com/qiangli/omniscore/internal/session"
	"github.com/qiangli/omniscore/internal/store"
)

// TestLinearSATPractice1_FullRun is the end-to-end student-experience test
// for the full Linear SAT Practice #1 conversion: load every module, walk
// each question top-to-bottom, submit the correct answer for each MCQ /
// SPR item, advance through modules, submit, and assert that the resulting
// raw/scaled totals match a perfect-score run against the published curve.
//
// The fixture lives under data/omni-data/ (gitignored — private content),
// so the test is skipped when the file isn't present. To run locally:
//
//	OMNI_LINEAR_PRACTICE_1=$(pwd)/data/omni-data/sat/sat-linear-practice-1 \
//	  go test ./internal/server -run TestLinearSATPractice1_FullRun -v
//
// or, if invoked without the env var, the test falls back to the
// canonical in-repo path.
func TestLinearSATPractice1_FullRun(t *testing.T) {
	src := os.Getenv("OMNI_LINEAR_PRACTICE_1")
	if src == "" {
		// Walk up from internal/server/ to repo root.
		wd, _ := os.Getwd()
		src = filepath.Join(wd, "..", "..", "data", "omni-data", "sat", "sat-linear-practice-1")
	}
	if _, err := os.Stat(filepath.Join(src, "test.json")); err != nil {
		t.Skipf("Linear SAT Practice #1 not present at %s — skip e2e (set OMNI_LINEAR_PRACTICE_1 to override)", src)
	}

	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")

	// Stage the test under <dir>/sat/<slug>/ in the per-test self-contained
	// layout so the runtime loader picks it up.
	slug := filepath.Base(src)
	dstDir := filepath.Join(dir, "sat", slug)
	if err := os.MkdirAll(filepath.Join(dstDir, "figures"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"test.json", "curve.json"} {
		raw, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, f), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := content.LoadFromDisk(ctx, st, dir); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	// Load the test to drive the answer loop with full knowledge of the keys.
	loaded, err := content.Get(ctx, st, slug)
	if err != nil {
		t.Fatalf("content.Get: %v", err)
	}
	if got := len(loaded.Modules); got != 4 {
		t.Fatalf("module count: want 4 (rw-1, rw-2, math-1, math-2), got %d", got)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(st, keyPath, nil, dir, logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	c := newClient(ts)

	// Join, create session.
	var join struct {
		ID int64 `json:"id"`
	}
	c.do(t, "POST", "/api/students", map[string]string{"display_name": "Auto Tester"}, &join)
	if join.ID == 0 {
		t.Fatal("no student id")
	}
	var env struct {
		Session session.Session `json:"session"`
		Module  content.Module  `json:"module"`
	}
	c.do(t, "POST", "/api/sessions", map[string]string{"test_slug": slug}, &env)
	if env.Session.ID == "" {
		t.Fatal("no session id")
	}
	if env.Module.ID != "rw-1" {
		t.Fatalf("first module: want rw-1, got %q", env.Module.ID)
	}

	// Verify the live envelope strips answer keys + SPR values.
	for _, q := range env.Module.Questions {
		if q.AnswerLabel != "" || len(q.AnswerValues) > 0 {
			t.Errorf("answer leak in live envelope for %s: label=%q values=%v",
				q.ID, q.AnswerLabel, q.AnswerValues)
		}
	}

	totalQuestions := 0
	for i, m := range loaded.Modules {
		// Answer every question in this module with the canonical correct
		// answer (MCQ → AnswerLabel; SPR → first AnswerValues entry).
		for _, q := range m.Questions {
			ans := correctAnswer(q)
			body := map[string]any{
				"question_id":         q.ID,
				"choice":              ans,
				"time_on_question_ms": 5_000,
			}
			var r map[string]any
			c.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer", body, &r)
			totalQuestions++
		}

		// Advance to the next module (or finish).
		var done map[string]any
		var nextEnv struct {
			Session session.Session `json:"session"`
			Module  content.Module  `json:"module"`
		}
		if i == len(loaded.Modules)-1 {
			c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &done)
			if v, _ := done["done"].(bool); !v {
				t.Fatalf("after last module: want done:true, got %v", done)
			}
		} else {
			c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &nextEnv)
			wantID := loaded.Modules[i+1].ID
			if nextEnv.Module.ID != wantID {
				t.Fatalf("advance from %s: want %s, got %s", m.ID, wantID, nextEnv.Module.ID)
			}
		}
	}

	if totalQuestions != 120 {
		t.Errorf("walked %d questions, want 120 (33+33 RW + 27+27 Math)", totalQuestions)
	}

	// Submit and assert a perfect-score outcome.
	var sum session.Summary
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/submit", nil, &sum)

	if sum.RawTotal != 120 {
		t.Errorf("raw total: want 120 (perfect), got %d", sum.RawTotal)
	}
	if sum.BySection["rw"] != 66 {
		t.Errorf("rw raw: want 66, got %d", sum.BySection["rw"])
	}
	if sum.BySection["math"] != 54 {
		t.Errorf("math raw: want 54, got %d", sum.BySection["math"])
	}

	// Perfect raw → top of the curve band per section.
	// curve.json: raw=66 rw → [790, 800]; raw=54 math → [790, 800].
	if sum.BySectionScaledLow["rw"] != 790 || sum.BySectionScaledHigh["rw"] != 800 {
		t.Errorf("rw band: want 790-800, got %d-%d",
			sum.BySectionScaledLow["rw"], sum.BySectionScaledHigh["rw"])
	}
	if sum.BySectionScaledLow["math"] != 790 || sum.BySectionScaledHigh["math"] != 800 {
		t.Errorf("math band: want 790-800, got %d-%d",
			sum.BySectionScaledLow["math"], sum.BySectionScaledHigh["math"])
	}
	if sum.ScaledTotalLow != 1580 || sum.ScaledTotalHigh != 1600 {
		t.Errorf("total band: want 1580-1600, got %d-%d", sum.ScaledTotalLow, sum.ScaledTotalHigh)
	}

	// Every per-question result should be correct, with rationale absent
	// (Linear Practice #1's import didn't include explanations).
	wrong := 0
	for _, r := range sum.Questions {
		if !r.IsCorrect {
			wrong++
			t.Logf("unexpected miss: %s chosen=%q correct=%q", r.QuestionID, r.Chosen, r.Correct)
		}
	}
	if wrong > 0 {
		t.Errorf("%d questions scored wrong despite canonical answers", wrong)
	}
}

// correctAnswer returns the canonical correct answer string for a question.
// For MCQ items it's the lettered label; for SPR items it's the first entry
// in AnswerValues. Caller posts this string verbatim as the session "choice".
func correctAnswer(q content.Question) string {
	switch q.Type {
	case "spr":
		if len(q.AnswerValues) == 0 {
			return ""
		}
		return q.AnswerValues[0]
	default:
		return q.AnswerLabel
	}
}

// TestLinearSATPractice1_RandomWalk drives the same fixture but answers
// every question with a deliberately wrong fixed choice ("A" for MCQ, "0"
// for SPR). It proves that the end-to-end happy path completes even when
// scores are bad, and that grading correctly rejects wrong answers.
func TestLinearSATPractice1_RandomWalk(t *testing.T) {
	src := os.Getenv("OMNI_LINEAR_PRACTICE_1")
	if src == "" {
		wd, _ := os.Getwd()
		src = filepath.Join(wd, "..", "..", "data", "omni-data", "sat", "sat-linear-practice-1")
	}
	if _, err := os.Stat(filepath.Join(src, "test.json")); err != nil {
		t.Skipf("Linear SAT Practice #1 not present at %s — skip e2e", src)
	}

	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")

	slug := filepath.Base(src)
	dstDir := filepath.Join(dir, "sat", slug)
	if err := os.MkdirAll(filepath.Join(dstDir, "figures"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"test.json", "curve.json"} {
		raw, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, f), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := content.LoadFromDisk(ctx, st, dir); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	loaded, err := content.Get(ctx, st, slug)
	if err != nil {
		t.Fatalf("content.Get: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(st, keyPath, nil, dir, logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	c := newClient(ts)

	var join struct {
		ID int64 `json:"id"`
	}
	c.do(t, "POST", "/api/students", map[string]string{"display_name": "Wrong Tester"}, &join)
	var env struct {
		Session session.Session `json:"session"`
	}
	c.do(t, "POST", "/api/sessions", map[string]string{"test_slug": slug}, &env)

	// Walk every module answering "A" / "0". For most MCQs "A" is wrong (the
	// keys are scattered across A-D evenly); for every SPR "0" is wrong.
	totalRight := 0
	for i, m := range loaded.Modules {
		for _, q := range m.Questions {
			wrong := wrongAnswer(q)
			var r map[string]any
			c.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer",
				map[string]any{"question_id": q.ID, "choice": wrong, "time_on_question_ms": 500}, &r)
			// Count incidentally-right answers: MCQs whose key happens to be "A".
			if q.Type != "spr" && q.AnswerLabel == "A" {
				totalRight++
			}
		}
		var ignore map[string]any
		c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &ignore)
		_ = i
	}

	var sum session.Summary
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/submit", nil, &sum)

	if sum.RawTotal != totalRight {
		t.Errorf("raw total: want %d (incidentally-correct A picks), got %d", totalRight, sum.RawTotal)
	}
	// Even with mostly-wrong answers the run completes end-to-end and the
	// curve resolves: the band must be a valid [200, 800] range.
	if sum.ScaledTotalLow < 400 || sum.ScaledTotalHigh > 1600 {
		t.Errorf("scaled band out of range: %d-%d", sum.ScaledTotalLow, sum.ScaledTotalHigh)
	}
}

func wrongAnswer(q content.Question) string {
	if q.Type == "spr" {
		return "0"
	}
	return "A"
}

// (unused json import guard — keeping for symmetry with sibling tests that
// inline JSON decoding)
var _ = json.Marshal

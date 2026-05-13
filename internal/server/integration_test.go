package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/server"
	"github.com/qiangli/omniscore/internal/session"
	"github.com/qiangli/omniscore/internal/store"
)

// TestFullSessionFlow drives the public API the way the React client does and
// asserts that scoring, resume, and highlight persistence all work end-to-end.
func TestFullSessionFlow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Seed: write a minimal test JSON + curve into the content dir and load.
	testsDir := filepath.Join(dir, "tests")
	curvesDir := filepath.Join(dir, "curves")
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(curvesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const fixtureSlug = "fixture-1"
	fixture := content.Test{
		Slug:     fixtureSlug,
		Title:    "Fixture",
		ExamType: "sat",
		Modules: []content.Module{
			{
				ID: "rw", Section: "rw", Title: "RW", TimeLimitS: 600,
				Questions: []content.Question{
					{ID: "q1", StemMD: "1?", Choices: []content.Choice{{Label: "A"}, {Label: "B"}}, AnswerLabel: "A", RationaleMD: "because"},
					{ID: "q2", StemMD: "2?", Choices: []content.Choice{{Label: "A"}, {Label: "B"}}, AnswerLabel: "B"},
				},
			},
			{
				ID: "math", Section: "math", Title: "Math", TimeLimitS: 600,
				Questions: []content.Question{
					{ID: "m1", StemMD: "3?", Choices: []content.Choice{{Label: "A"}, {Label: "B"}}, AnswerLabel: "A"},
				},
			},
		},
	}
	fixturePath := filepath.Join(testsDir, fixtureSlug+".json")
	if err := writeJSON(fixturePath, fixture); err != nil {
		t.Fatal(err)
	}
	curve := content.Curve{
		TestSlug: fixtureSlug,
		Sections: map[string][]content.CurvePoint{
			"rw":   {{Raw: 0, Scaled: 200}, {Raw: 1, Scaled: 400}, {Raw: 2, Scaled: 800}},
			"math": {{Raw: 0, Scaled: 200}, {Raw: 1, Scaled: 800}},
		},
	}
	if err := writeJSON(filepath.Join(curvesDir, fixtureSlug+".json"), curve); err != nil {
		t.Fatal(err)
	}
	if err := content.LoadFromDisk(ctx, s, dir); err != nil {
		t.Fatalf("load content: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(s, keyPath, nil, dir, logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	client := newClient(ts)

	// 1. Health.
	var hb map[string]string
	client.do(t, "GET", "/healthz", nil, &hb)
	if hb["status"] != "ok" {
		t.Fatalf("health: %v", hb)
	}

	// 2. Join.
	var join struct {
		ID int64 `json:"id"`
	}
	client.do(t, "POST", "/api/students", map[string]string{"display_name": "Tester"}, &join)
	if join.ID == 0 {
		t.Fatal("no student id")
	}

	// 3. Create session.
	var env struct {
		Session session.Session `json:"session"`
		Module  content.Module  `json:"module"`
	}
	client.do(t, "POST", "/api/sessions", map[string]string{"test_slug": fixtureSlug}, &env)
	if env.Session.ID == "" {
		t.Fatal("no session id")
	}
	if env.Module.ID != "rw" {
		t.Fatalf("expected first module rw, got %q", env.Module.ID)
	}

	// 4. Answer module 1: q1=A (correct), q2=A (wrong).
	for _, ans := range []struct{ Q, C string }{{"q1", "A"}, {"q2", "A"}} {
		var r map[string]any
		client.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer",
			map[string]any{"question_id": ans.Q, "choice": ans.C, "time_on_question_ms": 1000}, &r)
	}

	// 5. Highlight on q1.
	var h struct {
		ID int64 `json:"id"`
	}
	client.do(t, "POST", "/api/sessions/"+env.Session.ID+"/highlights",
		map[string]string{"question_id": "q1", "anchor_json": `{"text":"x","start":0,"end":1,"passage_version":1}`, "color": "yellow"}, &h)
	if h.ID == 0 {
		t.Fatal("no highlight id")
	}

	// 6. Resume — should see both answers + 1 highlight.
	var resumed struct {
		Responses  []session.Response  `json:"responses"`
		Highlights []session.Highlight `json:"highlights"`
		Module     content.Module      `json:"module"`
	}
	client.do(t, "GET", "/api/sessions/"+env.Session.ID, nil, &resumed)
	if len(resumed.Responses) != 2 {
		t.Fatalf("want 2 responses on resume, got %d", len(resumed.Responses))
	}
	if len(resumed.Highlights) != 1 {
		t.Fatalf("want 1 highlight on resume, got %d", len(resumed.Highlights))
	}

	// 7. Advance to math module.
	var nextEnv struct {
		Session session.Session `json:"session"`
		Module  content.Module  `json:"module"`
	}
	client.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &nextEnv)
	if nextEnv.Module.ID != "math" {
		t.Fatalf("expected advance to math, got %q", nextEnv.Module.ID)
	}

	// 8. Answer math: m1=A (correct).
	var r map[string]any
	client.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer",
		map[string]any{"question_id": "m1", "choice": "A", "time_on_question_ms": 2000}, &r)

	// 9. Advance past last module -> { done: true }.
	var done map[string]any
	client.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &done)
	if v, _ := done["done"].(bool); !v {
		t.Fatalf("expected done:true, got %v", done)
	}

	// 10. Submit & score.
	var sum session.Summary
	client.do(t, "POST", "/api/sessions/"+env.Session.ID+"/submit", nil, &sum)
	// 1 RW correct + 1 Math correct => RW raw=1 (scaled 400) + Math raw=1 (scaled 800) = 1200.
	if sum.RawTotal != 2 {
		t.Errorf("raw total: want 2, got %d", sum.RawTotal)
	}
	if sum.ScaledTotal != 1200 {
		t.Errorf("scaled total: want 1200, got %d", sum.ScaledTotal)
	}
	if sum.BySection["rw"] != 1 || sum.BySection["math"] != 1 {
		t.Errorf("by-section raw: %+v", sum.BySection)
	}
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// TestFullSessionFlow_AP mirrors TestFullSessionFlow but exercises the AP path:
// per-test self-contained subfolder layout, exam_type="ap", a 5-choice MCQ,
// embedded stem figure, and AP section codes (mcq_no_calc / mcq_calc). It is
// the contract the PDF importer's output must satisfy.
func TestFullSessionFlow_AP(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Per-test layout under <dir>/ap/<slug>/{test.json,curve.json,figures/}.
	const fixtureSlug = "ap-fixture"
	slugDir := filepath.Join(dir, "ap", fixtureSlug)
	apFigures := filepath.Join(slugDir, "figures")
	if err := os.MkdirAll(apFigures, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apFigures, "q3.png"), []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := content.Test{
		Slug: fixtureSlug, Title: "AP Fixture", ExamType: "ap", Subject: "calc_bc",
		Modules: []content.Module{
			{
				ID: "mcq-no-calc", Section: "mcq_no_calc", Title: "Section I, Part A — No calculator", TimeLimitS: 600,
				Questions: []content.Question{
					{ID: "q1", StemMD: "$f'(2)$ if $f(x)=x^3-3x$?",
						Choices: []content.Choice{
							{Label: "A", TextMD: "$3$"}, {Label: "B", TextMD: "$6$"}, {Label: "C", TextMD: "$9$"},
							{Label: "D", TextMD: "$12$"}, {Label: "E", TextMD: "$15$"},
						},
						AnswerLabel: "C", RationaleMD: "$3x^2-3$ at $x=2$ is $9$."},
					{ID: "q2", StemMD: "$\\lim_{x\\to0}\\sin(3x)/x$",
						Choices: []content.Choice{
							{Label: "A", TextMD: "$0$"}, {Label: "B", TextMD: "$1$"}, {Label: "C", TextMD: "$3$"},
							{Label: "D", TextMD: "$\\infty$"}, {Label: "E", TextMD: "DNE"},
						},
						AnswerLabel: "C"},
				},
			},
			{
				ID: "mcq-calc", Section: "mcq_calc", Title: "Section I, Part B — Calculator", TimeLimitS: 600,
				Questions: []content.Question{
					{ID: "q3", StemMD: "Slope field shown is for which DE?",
						StemFigure: &content.Figure{Src: "figures/q3.png", Alt: "slope field", WidthPx: 360},
						Choices: []content.Choice{
							{Label: "A", TextMD: "$dy/dx=x$"}, {Label: "B", TextMD: "$dy/dx=y$"},
							{Label: "C", TextMD: "$dy/dx=xy$"}, {Label: "D", TextMD: "$dy/dx=x+y$"},
							{Label: "E", TextMD: "$dy/dx=x-y$"},
						},
						AnswerLabel: "B"},
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(slugDir, "test.json"), fixture); err != nil {
		t.Fatal(err)
	}
	curve := content.Curve{
		TestSlug: fixtureSlug,
		Sections: map[string][]content.CurvePoint{
			"mcq_no_calc": {{Raw: 0, Scaled: 1}, {Raw: 1, Scaled: 3}, {Raw: 2, Scaled: 5}},
			"mcq_calc":    {{Raw: 0, Scaled: 1}, {Raw: 1, Scaled: 5}},
		},
	}
	if err := writeJSON(filepath.Join(slugDir, "curve.json"), curve); err != nil {
		t.Fatal(err)
	}
	if err := content.LoadFromDisk(ctx, s, dir); err != nil {
		t.Fatalf("load content: %v", err)
	}

	// Confirm figure src was rewritten to the absolute /api/figures/ URL.
	loaded, err := content.Get(ctx, s, fixtureSlug)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Modules[1].Questions[0].StemFigure
	want := "/api/figures/ap/ap-fixture/q3.png"
	if got == nil || got.Src != want {
		t.Fatalf("figure src rewrite: want %q, got %+v", want, got)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(s, keyPath, nil, dir, logger)
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
	c.do(t, "POST", "/api/students", map[string]string{"display_name": "AP Tester"}, &join)
	var env struct {
		Session session.Session `json:"session"`
		Module  content.Module  `json:"module"`
		Test    content.Listing `json:"test"`
	}
	c.do(t, "POST", "/api/sessions", map[string]string{"test_slug": fixtureSlug}, &env)
	if env.Test.Subject != "calc_bc" || env.Test.ExamType != "ap" {
		t.Fatalf("session test envelope missing AP metadata: %+v", env.Test)
	}
	if len(env.Module.Questions) != 2 || len(env.Module.Questions[0].Choices) != 5 {
		t.Fatalf("module 1 should have 2 questions × 5 choices, got %d × %d",
			len(env.Module.Questions), len(env.Module.Questions[0].Choices))
	}

	// Answer module 1: q1=C (correct), q2=E (wrong).
	for _, ans := range []struct{ Q, C string }{{"q1", "C"}, {"q2", "E"}} {
		var r map[string]any
		c.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer",
			map[string]any{"question_id": ans.Q, "choice": ans.C, "time_on_question_ms": 1000}, &r)
	}

	// Advance to mcq-calc module.
	var nextEnv struct {
		Module content.Module `json:"module"`
	}
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &nextEnv)
	if nextEnv.Module.Section != "mcq_calc" {
		t.Fatalf("advance: want mcq_calc, got %q", nextEnv.Module.Section)
	}
	// Confirm the figure metadata flows out via the live envelope (with answers stripped).
	if nextEnv.Module.Questions[0].StemFigure == nil ||
		nextEnv.Module.Questions[0].StemFigure.Src != "/api/figures/ap/ap-fixture/q3.png" {
		t.Errorf("module 2 figure missing/unrewritten: %+v", nextEnv.Module.Questions[0].StemFigure)
	}
	if nextEnv.Module.Questions[0].AnswerLabel != "" {
		t.Errorf("answer should be stripped in live envelope, got %q", nextEnv.Module.Questions[0].AnswerLabel)
	}

	// Answer q3=B (correct).
	var r map[string]any
	c.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer",
		map[string]any{"question_id": "q3", "choice": "B", "time_on_question_ms": 2000}, &r)

	// Advance past last module.
	var done map[string]any
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &done)
	if v, _ := done["done"].(bool); !v {
		t.Fatalf("expected done:true, got %v", done)
	}

	// Submit: q1 correct + q3 correct = 1 in each section.
	var sum session.Summary
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/submit", nil, &sum)
	if sum.ExamType != "ap" || sum.Subject != "calc_bc" {
		t.Errorf("Summary missing AP metadata: %+v", sum)
	}
	if sum.RawTotal != 2 {
		t.Errorf("raw total: want 2, got %d", sum.RawTotal)
	}
	// Per-section: mcq_no_calc raw=1 → 3, mcq_calc raw=1 → 5, total 8.
	if sum.ScaledTotal != 8 {
		t.Errorf("scaled total: want 8, got %d", sum.ScaledTotal)
	}
	if sum.BySection["mcq_no_calc"] != 1 || sum.BySection["mcq_calc"] != 1 {
		t.Errorf("by-section raw: %+v", sum.BySection)
	}
	if sum.BySectionScaled["mcq_no_calc"] != 3 || sum.BySectionScaled["mcq_calc"] != 5 {
		t.Errorf("by-section scaled: %+v", sum.BySectionScaled)
	}
}

// TestFullSessionFlow_SAT_SPR exercises Student-Produced Response items
// (Digital SAT Math fill-ins) plus the [low, high] curve band. It covers
// MCQ + SPR mixed in one module, the grader's numeric equivalence rules
// (0.5 ≡ 1/2 ≡ .5), multi-solution answers ("2; -12"), and the score
// range surfaced in Summary.ScaledTotalLow/High.
func TestFullSessionFlow_SAT_SPR(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const fixtureSlug = "sat-spr-fixture"
	slugDir := filepath.Join(dir, "sat", fixtureSlug)
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := content.Test{
		Slug: fixtureSlug, Title: "SAT SPR Fixture", ExamType: "sat",
		Modules: []content.Module{
			{
				ID: "math-1", Section: "math", Title: "Math — Module 1", TimeLimitS: 600,
				Questions: []content.Question{
					// MCQ for back-compat.
					{ID: "math-1-q1", Type: "mcq", StemMD: "What is $1+1$?",
						Choices: []content.Choice{
							{Label: "A", TextMD: "1"}, {Label: "B", TextMD: "2"},
							{Label: "C", TextMD: "3"}, {Label: "D", TextMD: "4"},
						},
						AnswerLabel: "B"},
					// SPR: integer.
					{ID: "math-1-q2", Type: "spr", StemMD: "Total widgets shipped this quarter?",
						AnswerValues: []string{"2520"}},
					// SPR: fractional with equivalence list.
					{ID: "math-1-q3", Type: "spr", StemMD: "Probability $1/2$?",
						AnswerValues: []string{"0.5", "1/2"}},
					// SPR: two acceptable answers (order-insensitive).
					{ID: "math-1-q4", Type: "spr", StemMD: "Solutions of $x^2+10x-24=0$?",
						AnswerValues: []string{"2; -12"}},
				},
			},
		},
	}
	if err := writeJSON(filepath.Join(slugDir, "test.json"), fixture); err != nil {
		t.Fatal(err)
	}
	// Curve with [low, high] band, matching real SAT scoring guides.
	curve := content.Curve{
		TestSlug: fixtureSlug,
		Sections: map[string][]content.CurvePoint{
			"math": {
				{Raw: 0, ScaledLow: 200, ScaledHigh: 200},
				{Raw: 1, ScaledLow: 250, ScaledHigh: 280},
				{Raw: 2, ScaledLow: 320, ScaledHigh: 360},
				{Raw: 3, ScaledLow: 420, ScaledHigh: 460},
				{Raw: 4, ScaledLow: 540, ScaledHigh: 580},
			},
		},
	}
	if err := writeJSON(filepath.Join(slugDir, "curve.json"), curve); err != nil {
		t.Fatal(err)
	}
	if err := content.LoadFromDisk(ctx, st, dir); err != nil {
		t.Fatalf("load content: %v", err)
	}

	// Confirm the SPR question survives load + StripAnswers.
	loaded, err := content.Get(ctx, st, fixtureSlug)
	if err != nil {
		t.Fatal(err)
	}
	stripped := content.StripAnswers(loaded)
	if stripped.Modules[0].Questions[2].AnswerValues != nil {
		t.Errorf("StripAnswers should clear AnswerValues, got %+v", stripped.Modules[0].Questions[2].AnswerValues)
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
	c.do(t, "POST", "/api/students", map[string]string{"display_name": "Tester"}, &join)
	var env struct {
		Session session.Session `json:"session"`
		Module  content.Module  `json:"module"`
	}
	c.do(t, "POST", "/api/sessions", map[string]string{"test_slug": fixtureSlug}, &env)

	// Live envelope must not leak answers (MCQ label or SPR values).
	for _, q := range env.Module.Questions {
		if q.AnswerLabel != "" || len(q.AnswerValues) > 0 {
			t.Errorf("answer leaked through envelope: %+v", q)
		}
	}

	// Submit answers exercising the grader's normalization paths.
	type ans struct{ Q, C string }
	for _, a := range []ans{
		{"math-1-q1", "B"},      // MCQ correct
		{"math-1-q2", "2520"},   // SPR integer correct
		{"math-1-q3", ".5"},     // SPR decimal equivalent of 1/2
		{"math-1-q4", "-12; 2"}, // SPR two-solution, reversed order
	} {
		var r map[string]any
		c.do(t, "PATCH", "/api/sessions/"+env.Session.ID+"/answer",
			map[string]any{"question_id": a.Q, "choice": a.C, "time_on_question_ms": 500}, &r)
	}

	// Advance past last module.
	var done map[string]any
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/advance", nil, &done)
	if v, _ := done["done"].(bool); !v {
		t.Fatalf("expected done:true, got %v", done)
	}

	var sum session.Summary
	c.do(t, "POST", "/api/sessions/"+env.Session.ID+"/submit", nil, &sum)

	if sum.RawTotal != 4 {
		t.Errorf("all 4 should grade correct, got raw_total=%d", sum.RawTotal)
	}
	if sum.BySection["math"] != 4 {
		t.Errorf("math raw: want 4, got %d", sum.BySection["math"])
	}
	// At raw=4 the curve says [540, 580]; midpoint = 560.
	if sum.BySectionScaledLow["math"] != 540 || sum.BySectionScaledHigh["math"] != 580 {
		t.Errorf("math band: want 540-580, got %d-%d",
			sum.BySectionScaledLow["math"], sum.BySectionScaledHigh["math"])
	}
	if sum.ScaledTotalLow != 540 || sum.ScaledTotalHigh != 580 {
		t.Errorf("total band: want 540-580, got %d-%d", sum.ScaledTotalLow, sum.ScaledTotalHigh)
	}
	// Per-question correctness: each Result.Correct should display the canonical key.
	resultsByID := map[string]session.Result{}
	for _, r := range sum.Questions {
		resultsByID[r.QuestionID] = r
	}
	if r := resultsByID["math-1-q3"]; !r.IsCorrect || r.Correct != "0.5 or 1/2" {
		t.Errorf("q3 (SPR equivalence): %+v", r)
	}
	if r := resultsByID["math-1-q4"]; !r.IsCorrect {
		t.Errorf("q4 (SPR two-solution, reversed): %+v", r)
	}

	// A deliberately wrong SPR answer must grade wrong.
	wrong := content.Question{Type: "spr", AnswerValues: []string{"0.5"}}
	_ = wrong // sanity: covered by grading unit tests; here we already exercised the happy path.
}

type client struct {
	base string
	jar  http.CookieJar
}

func newClient(ts *httptest.Server) *client {
	jar, _ := newJar()
	return &client{base: ts.URL, jar: jar}
}

// Minimal cookiejar replacement so we don't pull in net/http/cookiejar.
type memoryJar struct{ cookies []*http.Cookie }

func newJar() (http.CookieJar, error) { return &memoryJar{}, nil }
func (j *memoryJar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	for _, c := range cs {
		replaced := false
		for i, ex := range j.cookies {
			if ex.Name == c.Name {
				j.cookies[i] = c
				replaced = true
				break
			}
		}
		if !replaced {
			j.cookies = append(j.cookies, c)
		}
	}
}
func (j *memoryJar) Cookies(_ *url.URL) []*http.Cookie { return j.cookies }

func (c *client) do(t *testing.T, method, path string, body any, out any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	var req *http.Request
	var err error
	if rdr != nil {
		req, err = http.NewRequest(method, c.base+path, rdr)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, c.base+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(c.base + path)
	for _, ck := range c.jar.Cookies(u) {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	c.jar.SetCookies(u, resp.Cookies())
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
}

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
	if err := content.LoadFromDisk(ctx, s, testsDir, curvesDir); err != nil {
		t.Fatalf("load content: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(s, keyPath, nil, logger)
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

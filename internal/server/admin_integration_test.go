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
	"testing"

	"github.com/qiangli/omniscore/internal/admin"
	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/server"
	"github.com/qiangli/omniscore/internal/store"
)

// TestAdminReviewFlow exercises the full admin surface end-to-end:
//   - login with passphrase + whoami probe
//   - PATCH a question stem on disk; verify the file + hot-reloaded public API both reflect the edit
//   - bulk-approve every question; verify question_review rows are persisted
//   - reject calls without the admin cookie
func TestAdminReviewFlow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")
	adminKeyPath := filepath.Join(dir, "test.admin-key")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Per-test-subfolder layout is required for editing.
	const slug = "sat-fixture"
	slugDir := filepath.Join(dir, "sat", slug)
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := content.Test{
		Slug:     slug,
		Title:    "Admin Fixture",
		ExamType: "sat",
		Modules: []content.Module{{
			ID: "rw", Section: "rw", Title: "RW", TimeLimitS: 600,
			Questions: []content.Question{
				{ID: "q1", StemMD: "ORIGINAL_STEM", Choices: []content.Choice{{Label: "A", TextMD: "alpha"}, {Label: "B", TextMD: "bravo"}}, AnswerLabel: "A"},
				{ID: "q2", StemMD: "Q2?", Choices: []content.Choice{{Label: "A", TextMD: "a"}, {Label: "B", TextMD: "b"}}, AnswerLabel: "B"},
			},
		}},
	}
	if err := writeJSON(filepath.Join(slugDir, "test.json"), fixture); err != nil {
		t.Fatal(err)
	}
	if err := content.LoadFromDisk(ctx, s, dir); err != nil {
		t.Fatalf("load content: %v", err)
	}

	auth, passphrase, err := admin.NewAuth(adminKeyPath)
	if err != nil {
		t.Fatalf("admin.NewAuth: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(s, keyPath, nil, dir, auth, logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	c := newClient(ts)

	// Without cookie: whoami 401.
	if got := statusOf(t, c, "GET", "/api/admin/whoami", nil); got != http.StatusUnauthorized {
		t.Fatalf("whoami before login: want 401, got %d", got)
	}

	// Wrong passphrase: 401.
	if got := statusOf(t, c, "POST", "/api/admin/login", map[string]string{"passphrase": "obviously-wrong"}); got != http.StatusUnauthorized {
		t.Fatalf("login w/ bad pass: want 401, got %d", got)
	}

	// Correct passphrase: 200 + cookie set.
	var loginResp map[string]string
	c.do(t, "POST", "/api/admin/login", map[string]string{"passphrase": passphrase}, &loginResp)
	if loginResp["role"] != "admin" {
		t.Fatalf("login role: %v", loginResp)
	}

	var who map[string]string
	c.do(t, "GET", "/api/admin/whoami", nil, &who)
	if who["role"] != "admin" {
		t.Fatalf("whoami after login: %v", who)
	}

	// PATCH q1 stem. Verify on-disk JSON was rewritten + hot-reloaded API agrees.
	newStem := "EDITED_STEM"
	c.do(t, "PATCH", "/api/admin/tests/"+slug+"/questions/q1",
		map[string]any{"stem_md": newStem}, nil)

	raw, err := os.ReadFile(filepath.Join(slugDir, "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk content.Test
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("disk re-parse: %v", err)
	}
	if onDisk.Modules[0].Questions[0].StemMD != newStem {
		t.Fatalf("on-disk stem not updated: got %q", onDisk.Modules[0].Questions[0].StemMD)
	}

	// Hot-reload: public student-facing GET must see the new stem with NO server restart.
	var pub map[string]any
	c.do(t, "GET", "/api/tests/"+slug, nil, &pub)
	gotStem := pub["modules"].([]any)[0].(map[string]any)["questions"].([]any)[0].(map[string]any)["stem_md"]
	if gotStem != newStem {
		t.Fatalf("hot-reload failed: public stem=%v", gotStem)
	}

	// Bulk approve every question.
	var bulkResp map[string]any
	c.do(t, "POST", "/api/admin/tests/"+slug+"/review/bulk",
		map[string]any{"status": "approved"}, &bulkResp)
	if int(bulkResp["updated"].(float64)) != 2 {
		t.Fatalf("bulk updated count: %v", bulkResp)
	}

	// Confirm question_review rows persisted.
	var approved int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM question_review WHERE test_slug = ? AND status = 'approved'`, slug).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if approved != 2 {
		t.Fatalf("question_review approved rows: want 2, got %d", approved)
	}

	// Single-question PUT.
	c.do(t, "PUT", "/api/admin/tests/"+slug+"/questions/q1/review",
		map[string]any{"status": "flagged", "note": "double-check answer"}, nil)
	var flagStatus, flagNote string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT status, COALESCE(note, '') FROM question_review WHERE test_slug = ? AND question_id = 'q1'`, slug).Scan(&flagStatus, &flagNote); err != nil {
		t.Fatal(err)
	}
	if flagStatus != "flagged" || flagNote != "double-check answer" {
		t.Fatalf("single-q review: status=%q note=%q", flagStatus, flagNote)
	}

	// Sync status endpoint shape.
	var syncStatus map[string]any
	c.do(t, "GET", "/api/admin/sync/status", nil, &syncStatus)
	if _, ok := syncStatus["pending"]; !ok {
		t.Fatalf("sync status missing 'pending': %v", syncStatus)
	}

	// Logout invalidates the cookie.
	if got := statusOf(t, c, "POST", "/api/admin/logout", nil); got != http.StatusNoContent {
		t.Fatalf("logout: want 204, got %d", got)
	}
	if got := statusOf(t, c, "GET", "/api/admin/whoami", nil); got != http.StatusUnauthorized {
		t.Fatalf("whoami after logout: want 401, got %d", got)
	}
}

// TestAdminEditFlatLayoutRejected confirms the legacy flat layout is not
// editable — editor must return 409. Approval/flag still works because it
// lives in SQLite, not on disk.
func TestAdminEditFlatLayoutRejected(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")
	adminKeyPath := filepath.Join(dir, "test.admin-key")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const slug = "flat-fixture"
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := content.Test{
		Slug: slug, Title: "Flat", ExamType: "sat",
		Modules: []content.Module{{ID: "m", Section: "rw", Title: "M", TimeLimitS: 60,
			Questions: []content.Question{{ID: "q1", StemMD: "?", Choices: []content.Choice{{Label: "A"}}, AnswerLabel: "A"}}}},
	}
	if err := writeJSON(filepath.Join(dir, "tests", slug+".json"), fixture); err != nil {
		t.Fatal(err)
	}
	if err := content.LoadFromDisk(ctx, s, dir); err != nil {
		t.Fatal(err)
	}

	auth, passphrase, _ := admin.NewAuth(adminKeyPath)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, _ := server.New(s, keyPath, nil, dir, auth, logger)
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	c := newClient(ts)
	c.do(t, "POST", "/api/admin/login", map[string]string{"passphrase": passphrase}, nil)

	if got := statusOf(t, c, "PATCH", "/api/admin/tests/"+slug+"/questions/q1",
		map[string]any{"stem_md": "won't stick"}); got != http.StatusConflict {
		t.Fatalf("flat-layout edit: want 409, got %d", got)
	}

	// But approval still works since it lives in SQLite.
	c.do(t, "PUT", "/api/admin/tests/"+slug+"/questions/q1/review",
		map[string]any{"status": "approved"}, nil)
}

// statusOf does the request and returns the status code without failing
// on non-2xx. Use when the test specifically wants to assert a 4xx/5xx.
func statusOf(t *testing.T, c *client, method, path string, body any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
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
	c.jar.SetCookies(u, resp.Cookies())
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

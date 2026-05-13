package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/qiangli/omniscore/internal/server"
	"github.com/qiangli/omniscore/internal/store"
)

func TestServeFigure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "test.key")

	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Plant a real figure file at <dir>/ap/demo/figures/q1.png.
	figDir := filepath.Join(dir, "ap", "demo", "figures")
	if err := os.MkdirAll(figDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pngBytes := []byte("\x89PNG\r\n\x1a\nfake")
	if err := os.WriteFile(filepath.Join(figDir, "q1.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// Also plant a sensitive file outside the figures tree to attempt traversal against.
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(s, keyPath, nil, dir, logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   []byte
	}{
		{"happy path", "/api/figures/ap/demo/q1.png", http.StatusOK, pngBytes},
		{"missing file", "/api/figures/ap/demo/nonexistent.png", http.StatusNotFound, nil},
		{"traversal up", "/api/figures/ap/demo/../../../secret.txt", http.StatusBadRequest, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status: want %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			if tc.wantBody != nil {
				body, _ := io.ReadAll(resp.Body)
				if string(body) != string(tc.wantBody) {
					t.Fatalf("body: want %q, got %q", tc.wantBody, body)
				}
			}
		})
	}
}

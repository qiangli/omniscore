package render_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qiangli/omniscore/internal/importer/render"
)

// TestRasterizeIdempotent: pre-seed the outDir with two fake PNGs and confirm
// Rasterize returns them sorted without invoking pdftoppm. Lets us validate
// the cache path even on machines without Poppler installed.
func TestRasterizeIdempotent(t *testing.T) {
	outDir := t.TempDir()
	for _, name := range []string{"page-002.png", "page-001.png", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := render.Rasterize(context.Background(), "/non/existent.pdf", 200, outDir)
	if err != nil {
		t.Fatalf("Rasterize on pre-seeded dir: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 PNGs, got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "page-001.png" || filepath.Base(got[1]) != "page-002.png" {
		t.Fatalf("expected sorted page-NNN order, got: %v", got)
	}
}

// TestRasterizeMissingPdftoppm runs only when pdftoppm is NOT installed, just
// to confirm the error message is helpful.
func TestRasterizeMissingPdftoppm(t *testing.T) {
	t.Setenv("PATH", "/var/empty")
	outDir := t.TempDir()
	_, err := render.Rasterize(context.Background(), "/tmp/x.pdf", 200, outDir)
	if err == nil {
		t.Fatal("expected error when pdftoppm not on PATH")
	}
}

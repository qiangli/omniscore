// Package render rasterizes PDF pages to PNGs by shelling out to Poppler's
// pdftoppm. The importer uses two passes per PDF: a low-DPI pass for the page
// classifier (cheap), then a higher-DPI pass for question extraction.
package render

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Rasterize renders every page of pdfPath to <outDir>/page-NNN.png at the
// given dpi (e.g. 200 for classification, 300 for extraction). Returns the
// sorted list of generated PNG paths. Idempotent: if outDir already contains
// page-*.png files, they are returned without re-running pdftoppm.
//
// pdftoppm must be on $PATH. On macOS: brew install poppler.
func Rasterize(ctx context.Context, pdfPath string, dpi int, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("render: mkdir %s: %w", outDir, err)
	}

	if existing, err := list(outDir); err != nil {
		return nil, err
	} else if len(existing) > 0 {
		return existing, nil
	}

	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("render: pdftoppm not found on $PATH (install poppler): %w", err)
	}

	prefix := filepath.Join(outDir, "page")
	cmd := exec.CommandContext(ctx, "pdftoppm",
		"-png",
		"-r", fmt.Sprintf("%d", dpi),
		"-gray",
		pdfPath, prefix,
	)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("render: pdftoppm %s: %w (stderr: %s)", pdfPath, err, strings.TrimSpace(stderr.String()))
	}

	return list(outDir)
}

// list returns existing page-*.png files sorted by page number.
func list(outDir string) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, err
	}
	var pngs []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "page-") || !strings.HasSuffix(name, ".png") {
			continue
		}
		pngs = append(pngs, filepath.Join(outDir, name))
	}
	sort.Strings(pngs)
	return pngs, nil
}

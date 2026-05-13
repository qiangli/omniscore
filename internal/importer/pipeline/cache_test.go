package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManifestRoundTrip confirms LoadManifest reverses WriteManifest.
func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".import-manifest.json")
	m := ImportManifest{
		SchemaVersion: schemaVersion,
		ExamType:      "sat",
		Slug:          "demo",
		Title:         "Demo",
		Inputs: map[string]ManifestInput{
			"test": {Basename: "test.pdf", SHA256: "abc"},
		},
		Fingerprint:   "anthropic/claude-sonnet-4-6",
		DPIClassify:   200,
		DPIExtract:    300,
		ConsistencyN:  3,
		CompletedAtMS: 12345,
	}
	if err := WriteManifest(path, m); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadManifest(path)
	if err != nil || !ok {
		t.Fatalf("LoadManifest: ok=%v err=%v", ok, err)
	}
	if got.Slug != "demo" || got.Inputs["test"].SHA256 != "abc" {
		t.Errorf("unexpected manifest: %+v", got)
	}
}

func TestManifestMatches(t *testing.T) {
	base := ImportManifest{
		SchemaVersion: schemaVersion,
		ExamType:      "sat",
		Slug:          "demo",
		Inputs: map[string]ManifestInput{
			"test":    {Basename: "test.pdf", SHA256: "aaa"},
			"scoring": {Basename: "scoring.pdf", SHA256: "bbb"},
		},
		Fingerprint:  "model-x",
		DPIClassify:  200,
		DPIExtract:   300,
		ConsistencyN: 3,
	}
	cur := base // copy

	if !manifestMatches(base, cur) {
		t.Fatal("identical manifests should match")
	}

	// Hash drift → miss.
	cur2 := base
	cur2.Inputs = map[string]ManifestInput{
		"test":    {Basename: "test.pdf", SHA256: "ZZZ"},
		"scoring": {Basename: "scoring.pdf", SHA256: "bbb"},
	}
	if manifestMatches(base, cur2) {
		t.Error("hash drift should be a cache miss")
	}

	// Different number of inputs → miss.
	cur3 := base
	cur3.Inputs = map[string]ManifestInput{
		"test": {Basename: "test.pdf", SHA256: "aaa"},
	}
	if manifestMatches(base, cur3) {
		t.Error("input set change should be a cache miss")
	}

	// Fingerprint drift → miss.
	cur4 := base
	cur4.Fingerprint = "different-model"
	if manifestMatches(base, cur4) {
		t.Error("fingerprint drift should be a cache miss")
	}

	// Schema bump → miss.
	cur5 := base
	cur5.SchemaVersion = base.SchemaVersion + 99
	if manifestMatches(base, cur5) {
		t.Error("schema version bump should be a cache miss")
	}
}

func TestCopyRawPDFs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(src, []byte("%PDF-1.4 fake bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(dir, "out")

	// First call: copies.
	got, err := CopyRawPDFs(outRoot, "sat", "demo", map[string]string{"test": src})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 path, got %v", got)
	}
	wantDst := filepath.Join(outRoot, "sat", "demo", "raw", "in.pdf")
	if got[0] != wantDst {
		t.Errorf("dst path: want %q, got %q", wantDst, got[0])
	}
	stat1, err := os.Stat(wantDst)
	if err != nil {
		t.Fatal(err)
	}

	// Second call with same source: must be a no-op (no rewrite).
	got, err = CopyRawPDFs(outRoot, "sat", "demo", map[string]string{"test": src})
	if err != nil {
		t.Fatal(err)
	}
	stat2, err := os.Stat(wantDst)
	if err != nil {
		t.Fatal(err)
	}
	if stat1.ModTime() != stat2.ModTime() {
		t.Error("second copy should not rewrite an identical file")
	}
	_ = got

	// Mutated source: must re-copy.
	if err := os.WriteFile(src, []byte("%PDF-1.4 different bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyRawPDFs(outRoot, "sat", "demo", map[string]string{"test": src}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(wantDst)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "%PDF-1.4 different bytes" {
		t.Errorf("source mutation should re-copy; got %q", string(raw))
	}
}

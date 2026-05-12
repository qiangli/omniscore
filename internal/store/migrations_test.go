package store_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrationFilesIdentical guards against the two copies of the canonical
// migration drifting out of sync. internal/store/migrations/*.sql is what
// `go:embed` ships in the binary; migrations/*.sql at the repo root is the
// human-readable canonical copy referenced by README + CLAUDE.md.
func TestMigrationFilesIdentical(t *testing.T) {
	embedded := filepath.Join("migrations", "0001_init.sql")
	root := filepath.Join("..", "..", "migrations", "0001_init.sql")

	a, err := os.ReadFile(embedded)
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	b, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("migration drift: %s and %s differ — keep them byte-identical",
			embedded, root)
	}
}

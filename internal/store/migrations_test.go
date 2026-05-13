package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/qiangli/omniscore/internal/store"
)

// TestMigrationFilesIdentical guards against the two copies of the canonical
// migrations drifting out of sync. internal/store/migrations/*.sql is what
// `go:embed` ships in the binary; migrations/*.sql at the repo root is the
// human-readable canonical copy referenced by README + CLAUDE.md.
func TestMigrationFilesIdentical(t *testing.T) {
	pairs := [][2]string{
		{"migrations/0001_init.sql", "../../migrations/0001_init.sql"},
		{"migrations/0002_extensible_types.sql", "../../migrations/0002_extensible_types.sql"},
	}
	for _, p := range pairs {
		a, err := os.ReadFile(p[0])
		if err != nil {
			t.Fatalf("read embedded %s: %v", p[0], err)
		}
		b, err := os.ReadFile(p[1])
		if err != nil {
			t.Fatalf("read root %s: %v", p[1], err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("migration drift: %s and %s differ — keep them byte-identical", p[0], p[1])
		}
	}
}

// TestMigrationUpgradeFromV1 exercises the path that broke in production:
// a database that already has migration 0001 applied (including FK references
// from student_sessions and test_scoring_curves into test_templates) must
// survive the table-rebuild in 0002 without FK constraint failures.
//
// The migration runner toggles `PRAGMA foreign_keys` around each migration
// for exactly this reason — see internal/store/store.go.
func TestMigrationUpgradeFromV1(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")

	// Bootstrap a V1-only database by hand and populate it with rows that
	// hold FK references across all three referencing tables.
	raw, err := sql.Open("sqlite",
		"file:"+dbPath+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	v1SQL, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, string(v1SQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (1, 0)`); err != nil {
		t.Fatal(err)
	}
	// Seed: one test_template, one curve row, one student_session — all
	// holding FK refs into test_templates.slug.
	for _, stmt := range []string{
		`INSERT INTO test_templates VALUES ('demo','Demo','sat','{}',0)`,
		`INSERT INTO test_scoring_curves VALUES ('demo','rw',0,200)`,
		`INSERT INTO students(display_name, joined_at) VALUES ('alice', 0)`,
		`INSERT INTO student_sessions VALUES ('s1', 1, 'demo', 0, 0, 0, 'in_progress', NULL, NULL, 0, 0)`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	_ = raw.Close()

	// Open via the real Store — this is what `make dev` does. Migration
	// 0002 should apply without tripping the FK from student_sessions.
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open after V1 seed: %v", err)
	}
	defer s.Close()

	// Both migrations must show as applied.
	rows, err := s.DB.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if len(versions) != 2 || versions[0] != 1 || versions[1] != 2 {
		t.Fatalf("schema_migrations: want [1, 2], got %v", versions)
	}

	// FK referential integrity must survive the table rebuild.
	var cnt int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM student_sessions WHERE test_slug = 'demo'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("student_sessions row lost across migration: want 1, got %d", cnt)
	}

	// The relaxed CHECK should let us insert a new exam type that V1 banned.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO test_templates VALUES ('act-demo','ACT Demo','act','{}',0)`); err != nil {
		t.Errorf("post-migration: expected CHECK to be relaxed, but new exam_type still rejected: %v", err)
	}

	// The new curve range columns must be writable.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO test_scoring_curves(test_slug, section, raw_score, scaled_score, scaled_score_low, scaled_score_high) VALUES ('demo','rw',1,250,240,260)`); err != nil {
		t.Errorf("post-migration: scaled_score_low/high column missing: %v", err)
	}

	// FK enforcement must be ON after the migration finishes (the runner
	// turns it back on; verify by attempting an invalid FK reference).
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO test_scoring_curves(test_slug, section, raw_score, scaled_score) VALUES ('does-not-exist','rw',0,200)`); err == nil {
		t.Error("FK enforcement should be ON after migrations; insert with bad FK should have failed")
	}
}

// Package store is the SQLite-backed persistence layer for OmniScore.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a *sql.DB pinned to a SQLite file with WAL + FK enabled.
type Store struct {
	DB *sql.DB
}

// Open dials the SQLite file at path, applies pragmas, and runs embedded migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer; multi-reader handled by WAL
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	s := &Store{DB: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close releases the underlying DB handle.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate(ctx context.Context) error {
	// schema_migrations bootstrap
	if _, err := s.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	_ = rows.Close()

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	type mig struct {
		v   int
		f   string
		sql string
	}
	var migs []mig
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		under := strings.IndexByte(name, '_')
		if under < 1 {
			continue
		}
		v, err := strconv.Atoi(name[:under])
		if err != nil {
			continue
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return err
		}
		migs = append(migs, mig{v: v, f: name, sql: string(body)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].v < migs[j].v })

	for _, m := range migs {
		if applied[m.v] {
			continue
		}
		// SQLite's recommended pattern for migrations that rebuild tables
		// (DROP + CREATE same name) is to disable FK enforcement *outside*
		// the transaction, run the migration, verify integrity, then
		// re-enable. PRAGMA foreign_keys inside a transaction is silently
		// ignored, so the migration files can't toggle it themselves.
		if _, err := s.DB.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("disable FK before %s: %w", m.f, err)
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			_, _ = s.DB.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
			return fmt.Errorf("apply %s: %w", m.f, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, m.v, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			_, _ = s.DB.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
			return err
		}
		if err := tx.Commit(); err != nil {
			_, _ = s.DB.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
			return err
		}
		// Post-commit: verify FK integrity and turn enforcement back on.
		rows, ferr := s.DB.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if ferr != nil {
			return fmt.Errorf("foreign_key_check after %s: %w", m.f, ferr)
		}
		var violations []string
		for rows.Next() {
			var table string
			var rowid sql.NullInt64
			var parent string
			var fkid sql.NullInt64
			if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
				_ = rows.Close()
				return err
			}
			violations = append(violations, fmt.Sprintf("%s -> %s", table, parent))
		}
		_ = rows.Close()
		if len(violations) > 0 {
			return fmt.Errorf("FK violations after %s: %v", m.f, violations)
		}
		if _, err := s.DB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			return fmt.Errorf("re-enable FK after %s: %w", m.f, err)
		}
	}
	return nil
}

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/qiangli/omniscore/internal/content"
	"github.com/qiangli/omniscore/internal/store"
)

// ErrFlatLayout is returned when an admin tries to edit a test that ships
// in the legacy flat layout (content/tests/<slug>.json + curves/<slug>.json).
// The flat layout is read-only by design — admins can still approve/flag
// questions, but JSON edits require the per-test-subfolder layout.
var ErrFlatLayout = errors.New("admin: legacy flat layout is read-only; convert to per-test subfolder to enable editing")

// ErrTestNotOnDisk is returned when we have a slug in test_templates but no
// matching test.json under contentRoot. Happens if the DB and disk drift.
var ErrTestNotOnDisk = errors.New("admin: no test.json found on disk for that slug")

// JSONIO serializes JSON-on-disk edits and triggers the content loader so
// the running server sees changes without a restart. Single-process app,
// so an in-memory mutex is enough.
type JSONIO struct {
	contentRoot string
	store       *store.Store
	mu          sync.Mutex
}

// NewJSONIO wires the editor against the same -content path the server
// boots with. contentRoot may contain a mix of flat and per-test layouts;
// only per-test subfolders are editable.
func NewJSONIO(contentRoot string, s *store.Store) *JSONIO {
	return &JSONIO{contentRoot: contentRoot, store: s}
}

// LocateTest returns the on-disk path to test.json for slug, or
// ErrFlatLayout / ErrTestNotOnDisk.
func (j *JSONIO) LocateTest(slug string) (string, error) {
	// Walk <contentRoot>/<exam>/<slug>/test.json — the only editable layout.
	entries, err := os.ReadDir(j.contentRoot)
	if err != nil {
		return "", fmt.Errorf("read content root: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(j.contentRoot, e.Name(), slug, "test.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	// Flat layout fallback — present but not editable.
	flat := filepath.Join(j.contentRoot, "tests", slug+".json")
	if info, err := os.Stat(flat); err == nil && !info.IsDir() {
		return flat, ErrFlatLayout
	}
	return "", ErrTestNotOnDisk
}

// MutateAndSave reads test.json, applies fn to the parsed Test, writes
// atomically, then hot-reloads via content.LoadFromDisk. Returns the
// post-mutation Test.
func (j *JSONIO) MutateAndSave(ctx context.Context, slug string, fn func(*content.Test) error) (content.Test, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	path, err := j.LocateTest(slug)
	if err != nil {
		return content.Test{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return content.Test{}, fmt.Errorf("read %s: %w", path, err)
	}
	var t content.Test
	if err := json.Unmarshal(raw, &t); err != nil {
		return content.Test{}, fmt.Errorf("parse %s: %w", path, err)
	}
	// The loader rewrites figure srcs to absolute URLs before persisting
	// to SQLite. The on-disk file holds the relative form; the parsed
	// Test we just read is relative. fn operates on relative; reload
	// will rewrite to absolute again.
	if err := fn(&t); err != nil {
		return content.Test{}, err
	}
	out, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return content.Test{}, fmt.Errorf("marshal: %w", err)
	}
	if err := atomicWrite(path, out); err != nil {
		return content.Test{}, err
	}
	if err := content.LoadFromDisk(ctx, j.store, j.contentRoot); err != nil {
		return content.Test{}, fmt.Errorf("hot reload: %w", err)
	}
	return t, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, filepath.Base(path)+fmt.Sprintf(".tmp.%d.%d", os.Getpid(), time.Now().UnixNano()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp -> %s: %w", path, err)
	}
	return nil
}

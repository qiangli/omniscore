package sync_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/qiangli/omniscore/internal/store"
	omsync "github.com/qiangli/omniscore/internal/sync"
)

// memAdapter is a minimal Adapter that buffers pushes in-process and
// serves a fixed list of pulls. Enough to round-trip the loop in tests.
type memAdapter struct {
	pushed       []omsync.Change
	pullChanges  map[string][]omsync.Change
	pullCursors  map[string]string
	failNextPush bool
}

func (m *memAdapter) Name() string { return "mem" }

func (m *memAdapter) Push(_ context.Context, batch []omsync.Change) ([]omsync.Ack, error) {
	if m.failNextPush {
		m.failNextPush = false
		return nil, errors.New("simulated transport failure")
	}
	acks := make([]omsync.Ack, len(batch))
	for i, c := range batch {
		m.pushed = append(m.pushed, c)
		acks[i] = omsync.Ack{OutboxID: c.OutboxID, OK: true}
	}
	return acks, nil
}

func (m *memAdapter) Pull(_ context.Context, entityType, cursor string) ([]omsync.Change, string, error) {
	if cursor == m.pullCursors[entityType] {
		return nil, cursor, nil
	}
	return m.pullChanges[entityType], m.pullCursors[entityType], nil
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sync.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNoopLeavesOutboxPending(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if err := omsync.Enqueue(ctx, s.DB, omsync.EntityUser, "u1", omsync.OpUpsert, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	batch, err := omsync.DrainBatch(ctx, s.DB, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 {
		t.Fatalf("want 1 pending row, got %d", len(batch))
	}
	if _, err := (omsync.Noop{}).Push(ctx, batch); !errors.Is(err, omsync.ErrNotConfigured) {
		t.Fatalf("Noop.Push: want ErrNotConfigured, got %v", err)
	}
	// Outbox still has the row.
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_outbox WHERE delivered_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("noop must not drain outbox: pending=%d", n)
	}
}

func TestPushRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := omsync.Enqueue(ctx, s.DB, omsync.EntityUser, "u1", omsync.OpUpsert, []byte(`{"name":"alice"}`)); err != nil {
		t.Fatal(err)
	}
	ad := &memAdapter{}
	batch, _ := omsync.DrainBatch(ctx, s.DB, 16)
	acks, err := ad.Push(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := omsync.MarkAcked(ctx, s.DB, acks); err != nil {
		t.Fatal(err)
	}
	var pending int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_outbox WHERE delivered_at IS NULL`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("pending after ack: want 0, got %d", pending)
	}
	if len(ad.pushed) != 1 || ad.pushed[0].EntityID != "u1" {
		t.Fatalf("adapter did not receive the row: %+v", ad.pushed)
	}
}

func TestPullAppliesAndAdvancesCursor(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	ad := &memAdapter{
		pullChanges: map[string][]omsync.Change{
			omsync.EntityUser: {{EntityType: omsync.EntityUser, EntityID: "u-ext-1", Op: omsync.OpUpsert, Payload: []byte(`{"role":"teacher","name":"bob"}`)}},
		},
		pullCursors: map[string]string{omsync.EntityUser: "v1"},
	}
	applier := func(ctx context.Context, tx *sql.Tx, c omsync.Change) error {
		_, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO users(id, role, name, created_at) VALUES (?, 'teacher', 'bob', 0)`, c.EntityID)
		return err
	}
	cur, _ := omsync.GetCursor(ctx, s.DB, omsync.EntityUser)
	changes, newCur, err := ad.Pull(ctx, omsync.EntityUser, cur)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := s.DB.BeginTx(ctx, nil)
	for _, c := range changes {
		if err := applier(ctx, tx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := omsync.SetCursor(ctx, s.DB, omsync.EntityUser, newCur); err != nil {
		t.Fatal(err)
	}

	var name string
	if err := s.DB.QueryRowContext(ctx, `SELECT name FROM users WHERE id = 'u-ext-1'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "bob" {
		t.Fatalf("applied user name: want bob, got %q", name)
	}
	got, _ := omsync.GetCursor(ctx, s.DB, omsync.EntityUser)
	if got != "v1" {
		t.Fatalf("cursor advanced: want v1, got %q", got)
	}
}

func TestPushFailureRecordsLastError(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := omsync.Enqueue(ctx, s.DB, omsync.EntityUser, "u1", omsync.OpUpsert, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	ad := &memAdapter{failNextPush: true}
	batch, _ := omsync.DrainBatch(ctx, s.DB, 16)
	_, err := ad.Push(ctx, batch)
	if err == nil {
		t.Fatal("expected push failure")
	}
	if err := omsync.MarkBatchFailed(ctx, s.DB, batch, err); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var lastErr sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT attempts, last_error FROM sync_outbox WHERE id = ?`, batch[0].OutboxID).Scan(&attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("attempts: want 1, got %d", attempts)
	}
	if lastErr.String == "" {
		t.Fatal("last_error should be set after a failed push")
	}
}

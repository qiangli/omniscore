package sync

import (
	"context"
	"database/sql"
	"time"
)

// execer is satisfied by both *sql.DB and *sql.Tx so callers can enqueue
// from inside an existing transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Enqueue adds one outbox row. Pass a *sql.Tx when the caller wants the
// enqueue to commit atomically with the entity write that triggered it.
func Enqueue(ctx context.Context, x execer, entityType, entityID, op string, payload []byte) error {
	_, err := x.ExecContext(ctx,
		`INSERT INTO sync_outbox(entity_type, entity_id, op, payload_json, enqueued_at)
		 VALUES (?, ?, ?, ?, ?)`,
		entityType, entityID, op, string(payload), time.Now().UnixMilli())
	return err
}

// DrainBatch reads up to limit pending outbox rows ordered by id.
func DrainBatch(ctx context.Context, db *sql.DB, limit int) ([]Change, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, entity_type, entity_id, op, payload_json
		   FROM sync_outbox
		  WHERE delivered_at IS NULL
		  ORDER BY id
		  LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var payload string
		if err := rows.Scan(&c.OutboxID, &c.EntityType, &c.EntityID, &c.Op, &payload); err != nil {
			return nil, err
		}
		c.Payload = []byte(payload)
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkAcked stamps delivered_at for successful rows; for failures it
// increments attempts and records last_error so callers can see what
// went wrong without spelunking logs.
func MarkAcked(ctx context.Context, db *sql.DB, acks []Ack) error {
	if len(acks) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, a := range acks {
		if a.OK {
			if _, err := tx.ExecContext(ctx,
				`UPDATE sync_outbox SET delivered_at = ?, last_error = NULL WHERE id = ?`,
				now, a.OutboxID); err != nil {
				_ = tx.Rollback()
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE sync_outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
			a.Err, a.OutboxID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// MarkBatchFailed records a transport-level error for every row in the
// batch when Push itself returns an error (i.e. the adapter never got
// individual acks back).
func MarkBatchFailed(ctx context.Context, db *sql.DB, batch []Change, err error) error {
	if len(batch) == 0 {
		return nil
	}
	tx, txErr := db.BeginTx(ctx, nil)
	if txErr != nil {
		return txErr
	}
	for _, c := range batch {
		if _, e := tx.ExecContext(ctx,
			`UPDATE sync_outbox SET attempts = attempts + 1, last_error = ? WHERE id = ?`,
			err.Error(), c.OutboxID); e != nil {
			_ = tx.Rollback()
			return e
		}
	}
	return tx.Commit()
}

// GetCursor returns the stored cursor for entityType (empty string if absent).
func GetCursor(ctx context.Context, db *sql.DB, entityType string) (string, error) {
	var cur sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT cursor FROM sync_cursor WHERE entity_type = ?`, entityType).Scan(&cur)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return cur.String, nil
}

// SetCursor upserts the cursor for entityType.
func SetCursor(ctx context.Context, db *sql.DB, entityType, cursor string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO sync_cursor(entity_type, cursor, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(entity_type) DO UPDATE SET cursor = excluded.cursor, updated_at = excluded.updated_at`,
		entityType, cursor, time.Now().UnixMilli())
	return err
}

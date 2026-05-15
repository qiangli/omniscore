package sync

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

// Applier turns an inbound Change into a write against the local DB.
// One Applier per entity type; the loop dispatches via the map passed to
// Run. Applying happens in its own transaction per pull batch.
type Applier func(ctx context.Context, tx *sql.Tx, c Change) error

// Config controls the loop. Zero values get sensible defaults.
type Config struct {
	Adapter   Adapter
	DB        *sql.DB
	Interval  time.Duration
	BatchSize int
	// PullEntities lists the entity types the loop should pull on each
	// tick, in order. Adapters that only push (e.g. outbound-only
	// integrations) pass an empty slice.
	PullEntities []string
	// Appliers handles inbound changes by entity type. Missing entries
	// cause the change to be logged and skipped — never silently
	// dropped without a record.
	Appliers map[string]Applier
}

// Run drives the loop until ctx is cancelled. It is safe to call with a
// Noop adapter; tick() then no-ops on ErrNotConfigured.
func Run(ctx context.Context, cfg Config) {
	if cfg.Adapter == nil {
		log.Printf("sync: no adapter configured, loop not started")
		return
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 64
	}
	log.Printf("sync: starting loop adapter=%s interval=%s", cfg.Adapter.Name(), cfg.Interval)

	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		if err := tick(ctx, cfg); err != nil && !errors.Is(err, ErrNotConfigured) {
			log.Printf("sync: tick: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func tick(ctx context.Context, cfg Config) error {
	// Push outbound.
	batch, err := DrainBatch(ctx, cfg.DB, cfg.BatchSize)
	if err != nil {
		return err
	}
	if len(batch) > 0 {
		acks, perr := cfg.Adapter.Push(ctx, batch)
		switch {
		case errors.Is(perr, ErrNotConfigured):
			return perr
		case perr != nil:
			if merr := MarkBatchFailed(ctx, cfg.DB, batch, perr); merr != nil {
				return merr
			}
		default:
			if merr := MarkAcked(ctx, cfg.DB, acks); merr != nil {
				return merr
			}
		}
	}

	// Pull inbound for each entity type the caller cares about.
	for _, et := range cfg.PullEntities {
		cur, err := GetCursor(ctx, cfg.DB, et)
		if err != nil {
			return err
		}
		changes, newCur, perr := cfg.Adapter.Pull(ctx, et, cur)
		switch {
		case errors.Is(perr, ErrNotConfigured):
			return perr
		case perr != nil:
			log.Printf("sync: pull %s: %v", et, perr)
			continue
		}
		if len(changes) == 0 && newCur == cur {
			continue
		}
		if err := apply(ctx, cfg, changes, et, newCur); err != nil {
			log.Printf("sync: apply %s: %v", et, err)
		}
	}
	return nil
}

func apply(ctx context.Context, cfg Config, changes []Change, entityType, newCursor string) error {
	tx, err := cfg.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, c := range changes {
		fn, ok := cfg.Appliers[c.EntityType]
		if !ok {
			log.Printf("sync: no applier for entity_type=%s id=%s op=%s — skipping", c.EntityType, c.EntityID, c.Op)
			continue
		}
		if err := fn(ctx, tx, c); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return SetCursor(ctx, cfg.DB, entityType, newCursor)
}

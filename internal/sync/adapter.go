// Package sync provides the offline-first sync layer for OmniScore.
//
// Local writes always succeed and land in the sync_outbox table inside the
// same transaction as the entity write. A pluggable Adapter ships outbox
// rows to an external system when one is reachable and pulls inbound
// changes back. The default Noop adapter makes the outbox accumulate
// safely until a real adapter is wired up — no wire format is committed
// to here.
package sync

import (
	"context"
	"errors"
)

// EntityType values used in sync_outbox.entity_type and sync_cursor.entity_type.
// Keep the set explicit so adapters know what to expect.
const (
	EntityUser         = "user"
	EntityStandardTest = "standard_test"
	EntityTask         = "task"
)

// Op values.
const (
	OpUpsert = "upsert"
	OpDelete = "delete"
)

// Change is one outbox row on its way out, or one inbound change on its
// way in. Payload is opaque to the loop — the adapter and the apply
// functions agree on its shape.
type Change struct {
	OutboxID   int64
	EntityType string
	EntityID   string
	Op         string
	Payload    []byte
	Etag       string
}

// Ack reports the outcome of a single outbox row in a Push batch.
type Ack struct {
	OutboxID int64
	OK       bool
	Err      string
}

// Adapter shuttles changes between OmniScore and an external system.
// Implementations must be safe for concurrent use; the loop calls Push
// and Pull serially today but that is not a contract.
type Adapter interface {
	Name() string
	Push(ctx context.Context, batch []Change) ([]Ack, error)
	Pull(ctx context.Context, entityType, cursor string) (changes []Change, newCursor string, err error)
}

// ErrNotConfigured is returned by Noop so the loop knows there is nothing
// to do and leaves the outbox untouched.
var ErrNotConfigured = errors.New("sync: no external adapter configured")

// Noop is the default adapter. It refuses both Push and Pull so that
// outbox rows accumulate until a real adapter takes over.
type Noop struct{}

func (Noop) Name() string                                            { return "noop" }
func (Noop) Push(context.Context, []Change) ([]Ack, error)           { return nil, ErrNotConfigured }
func (Noop) Pull(context.Context, string, string) ([]Change, string, error) {
	return nil, "", ErrNotConfigured
}

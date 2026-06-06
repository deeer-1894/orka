// Package checkpoint implements the runtime checkpoint store binding cavis_core's
// CheckpointStore to redis, with TTL (human-delay tolerance) and CAS/claim
// semantics that make Clarify resume idempotent.
package checkpoint

import (
	"context"

	cp "github.com/cavis-oss/cavis_core/checkpoint"
)

// Store is the control-layer checkpoint contract: the cavis_core interface plus
// Claim, an atomic load+delete used to guarantee a checkpoint is resumed once.
type Store interface {
	Save(ctx context.Context, key string, c *cp.Checkpoint) error
	Load(ctx context.Context, key string) (*cp.Checkpoint, error)
	Delete(ctx context.Context, key string) error
	// Claim atomically returns and removes the checkpoint. A second concurrent
	// or duplicate claim gets ErrNotFound — this is the resume idempotency guard.
	Claim(ctx context.Context, key string) (*cp.Checkpoint, error)
}

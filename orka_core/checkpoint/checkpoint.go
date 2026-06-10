// Package checkpoint defines the interface and type for serializing runtime
// state to support Clarify resume. The concrete redis implementation lives in
// the control layer (which owns the runtime resume semantics).
package checkpoint

import (
	"context"
	"errors"

	"github.com/orka-oss/orka_core/messages"
)

// Errors.
var (
	ErrNotFound        = errors.New("checkpoint: not found")
	ErrVersionConflict = errors.New("checkpoint: version conflict (concurrent/duplicate resume)")
)

// Checkpoint is the serialized runtime state captured at an interrupt point.
type Checkpoint struct {
	Messages  []messages.Message `json:"messages"`   // full history
	Cursor    int                `json:"cursor"`     // middleware position
	Vars      map[string]any     `json:"vars"`       // intermediate variables
	Meta      messages.Meta      `json:"meta"`       // session metadata
	Version   int                `json:"version"`    // CAS guard against duplicate resume
	CreatedAt int64              `json:"created_at"` // unix millis
	TTLSec    int                `json:"ttl_sec"`    // human-delay tolerance
}

// CheckpointStore persists checkpoints with CAS + TTL semantics.
type CheckpointStore interface {
	// Save persists cp under key. Implementations enforce optimistic
	// concurrency on cp.Version (returns ErrVersionConflict on mismatch).
	Save(ctx context.Context, key string, cp *Checkpoint) error
	Load(ctx context.Context, key string) (*Checkpoint, error)
	Delete(ctx context.Context, key string) error
}

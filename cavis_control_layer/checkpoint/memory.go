package checkpoint

import (
	"context"
	"encoding/json"
	"sync"

	cp "github.com/cavis-oss/cavis_core/checkpoint"
)

// MemoryStore is an in-process Store for tests. It JSON round-trips values so
// resume exercises the same "generic Vars" decoding path as redis.
type MemoryStore struct {
	mu sync.Mutex
	m  map[string]memEntry
}

type memEntry struct {
	raw     []byte
	version int
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]memEntry{}} }

func (s *MemoryStore) Save(_ context.Context, key string, c *cp.Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.m[key]
	switch {
	case !ok:
		if c.Version != 1 {
			return cp.ErrVersionConflict
		}
	default:
		if c.Version != cur.version+1 {
			return cp.ErrVersionConflict
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	s.m[key] = memEntry{raw: b, version: c.Version}
	return nil
}

func (s *MemoryStore) Load(_ context.Context, key string) (*cp.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return nil, cp.ErrNotFound
	}
	return decode(e.raw)
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *MemoryStore) Claim(_ context.Context, key string) (*cp.Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return nil, cp.ErrNotFound
	}
	delete(s.m, key)
	return decode(e.raw)
}

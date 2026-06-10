// Package state provides a key-value state abstraction with memory and redis
// implementations. It is the generic substrate the control layer binds runtime
// resume semantics onto (state storage decoupled from business recovery logic).
package state

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNotFound is returned when a key is absent.
var ErrNotFound = errors.New("state: key not found")

// StateStore is a TTL-aware byte KV store.
type StateStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type memItem struct {
	val    []byte
	expire time.Time // zero = never expires
}

// MemoryStore is an in-process StateStore (useful for tests and single-node).
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]memItem
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]memItem{}} }

func (s *MemoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	it, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	if !it.expire.IsZero() && time.Now().After(it.expire) {
		_ = s.Delete(ctx, key)
		return nil, ErrNotFound
	}
	out := make([]byte, len(it.val))
	copy(out, it.val)
	return out, nil
}

func (s *MemoryStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	cp := make([]byte, len(val))
	copy(cp, val)
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	s.mu.Lock()
	s.m[key] = memItem{val: cp, expire: exp}
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	return nil
}

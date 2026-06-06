package checkpoint

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	cp "github.com/cavis-oss/cavis_core/checkpoint"
	"github.com/cavis-oss/cavis_core/messages"
)

func sampleCP(version int) *cp.Checkpoint {
	return &cp.Checkpoint{
		Messages: []messages.Message{messages.Chat(messages.RoleUser, "hi", messages.Meta{})},
		Cursor:   1,
		Vars:     map[string]any{"k": "v"},
		Version:  version,
		TTLSec:   3600,
	}
}

// shared CAS + claim behavior, exercised against any Store impl.
func runStoreContract(t *testing.T, s Store) {
	ctx := context.Background()
	key := "k-" + messages.NewID()

	// load missing -> ErrNotFound
	if _, err := s.Load(ctx, key); err != cp.ErrNotFound {
		t.Fatalf("load missing: want ErrNotFound, got %v", err)
	}
	// first save must be version 1
	if err := s.Save(ctx, key, sampleCP(2)); err != cp.ErrVersionConflict {
		t.Fatalf("save v2 on empty: want ErrVersionConflict, got %v", err)
	}
	if err := s.Save(ctx, key, sampleCP(1)); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	// re-saving v1 (not advancing) conflicts
	if err := s.Save(ctx, key, sampleCP(1)); err != cp.ErrVersionConflict {
		t.Fatalf("re-save v1: want ErrVersionConflict, got %v", err)
	}
	// advancing to v2 ok
	if err := s.Save(ctx, key, sampleCP(2)); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	// load round-trips
	got, err := s.Load(ctx, key)
	if err != nil || got.Cursor != 1 || got.Version != 2 {
		t.Fatalf("load: %+v err=%v", got, err)
	}
	// claim returns and removes
	claimed, err := s.Claim(ctx, key)
	if err != nil || claimed.Version != 2 {
		t.Fatalf("claim: %+v err=%v", claimed, err)
	}
	// second claim -> ErrNotFound (idempotency guard)
	if _, err := s.Claim(ctx, key); err != cp.ErrNotFound {
		t.Fatalf("second claim: want ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_Contract(t *testing.T) {
	runStoreContract(t, NewMemoryStore())
}

func TestRedisStore_Contract(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unreachable at %s: %v", addr, err)
	}
	defer c.Close()
	runStoreContract(t, NewRedisStore(c, "cavis:test:cp:"))
}

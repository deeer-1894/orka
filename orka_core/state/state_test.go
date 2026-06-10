package state

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestMemoryStore_SetGetDelete(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	if _, err := s.Get(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "k"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStore_TTLExpiry(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Set(ctx, "k", []byte("v"), 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "k"); err != nil {
		t.Fatalf("should exist before expiry: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := s.Get(ctx, "k"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after ttl, got %v", err)
	}
}

func TestMemoryStore_IsolatesCallerSlice(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	in := []byte("abc")
	_ = s.Set(ctx, "k", in, 0)
	in[0] = 'X' // mutate caller's slice
	got, _ := s.Get(ctx, "k")
	if string(got) != "abc" {
		t.Fatalf("store did not copy on set: %q", got)
	}
	got[0] = 'Y' // mutate returned slice
	again, _ := s.Get(ctx, "k")
	if string(again) != "abc" {
		t.Fatalf("store did not copy on get: %q", again)
	}
}

// RedisStore test runs against REDIS_ADDR (default localhost:6379); skips if unreachable.
func TestRedisStore(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unreachable at %s: %v", addr, err)
	}
	defer c.Close()

	s := NewRedisStore(c, "orka:test:state:")
	key := "k-" + time.Now().Format("150405.000")
	defer s.Delete(context.Background(), key)

	if _, err := s.Get(context.Background(), key); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.Set(context.Background(), key, []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), key)
	if err != nil || string(got) != "v" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), key); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

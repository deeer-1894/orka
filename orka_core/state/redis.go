package state

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore is a redis-backed StateStore. All keys are namespaced by prefix.
type RedisStore struct {
	c      *redis.Client
	prefix string
}

// NewRedisStore wraps a redis client. prefix is prepended to every key.
func NewRedisStore(c *redis.Client, prefix string) *RedisStore {
	return &RedisStore{c: c, prefix: prefix}
}

func (s *RedisStore) k(key string) string { return s.prefix + key }

func (s *RedisStore) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := s.c.Get(ctx, s.k(key)).Bytes()
	if err == redis.Nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get %q: %w", key, err)
	}
	return b, nil
}

func (s *RedisStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if err := s.c.Set(ctx, s.k(key), val, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}

func (s *RedisStore) Delete(ctx context.Context, key string) error {
	if err := s.c.Del(ctx, s.k(key)).Err(); err != nil {
		return fmt.Errorf("redis del %q: %w", key, err)
	}
	return nil
}

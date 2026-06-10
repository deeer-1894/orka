package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	cp "github.com/orka-oss/orka_core/checkpoint"
)

// RedisStore persists checkpoints in redis.
type RedisStore struct {
	c      *redis.Client
	prefix string
}

// NewRedisStore wraps a redis client; keys are namespaced by prefix.
func NewRedisStore(c *redis.Client, prefix string) *RedisStore {
	return &RedisStore{c: c, prefix: prefix}
}

func (s *RedisStore) k(key string) string { return s.prefix + key }

// saveCAS enforces optimistic concurrency on the embedded version: a new key
// must have version 1; an existing key must be advanced by exactly one.
var saveCAS = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
local newver = tonumber(ARGV[2])
if cur == false then
  if newver ~= 1 then return redis.error_reply('VERSION_CONFLICT') end
else
  local ok, obj = pcall(cjson.decode, cur)
  local curver = 0
  if ok and obj and obj.version ~= nil then curver = obj.version end
  if newver ~= curver + 1 then return redis.error_reply('VERSION_CONFLICT') end
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[3]))
return 'OK'
`)

func (s *RedisStore) Save(ctx context.Context, key string, c *cp.Checkpoint) error {
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().UnixMilli()
	}
	ttl := c.TTLSec
	if ttl <= 0 {
		ttl = 86400
	}
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	err = saveCAS.Run(ctx, s.c, []string{s.k(key)}, string(b), c.Version, ttl).Err()
	if err != nil {
		if strings.Contains(err.Error(), "VERSION_CONFLICT") {
			return cp.ErrVersionConflict
		}
		return fmt.Errorf("checkpoint save: %w", err)
	}
	return nil
}

func (s *RedisStore) Load(ctx context.Context, key string) (*cp.Checkpoint, error) {
	b, err := s.c.Get(ctx, s.k(key)).Bytes()
	if err == redis.Nil {
		return nil, cp.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint load: %w", err)
	}
	return decode(b)
}

func (s *RedisStore) Delete(ctx context.Context, key string) error {
	if err := s.c.Del(ctx, s.k(key)).Err(); err != nil {
		return fmt.Errorf("checkpoint delete: %w", err)
	}
	return nil
}

// Claim atomically returns and removes the checkpoint (GETDEL). A second claim
// returns ErrNotFound, making resume idempotent.
func (s *RedisStore) Claim(ctx context.Context, key string) (*cp.Checkpoint, error) {
	b, err := s.c.GetDel(ctx, s.k(key)).Bytes()
	if err == redis.Nil {
		return nil, cp.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint claim: %w", err)
	}
	return decode(b)
}

func decode(b []byte) (*cp.Checkpoint, error) {
	var out cp.Checkpoint
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode checkpoint: %w", err)
	}
	return &out, nil
}

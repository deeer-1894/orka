package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// Cached clients contain credentials, so bound both count and idle lifetime.
// Borrow on every call, not once per snapshot: an old snapshot returning after
// eviction must join the current limiter rather than keep a second semaphore.
// Active and queued calls pin their entry until completion.
type modelClientPool struct {
	mu       sync.Mutex
	entries  map[string]*modelClientEntry
	capacity int
	idleTTL  time.Duration
}
type modelClientEntry struct {
	client llm.Client
	users  int
	last   time.Time
	timer  *time.Timer
}

func (p *modelClientPool) acquire(identity string, create func() llm.Client) (llm.Client, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	capacity := p.capacity
	if capacity <= 0 {
		capacity = 128
	}
	ttl := p.idleTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if p.entries == nil {
		p.entries = make(map[string]*modelClientEntry)
	}
	e := p.entries[identity]
	if e == nil {
		if len(p.entries) >= capacity {
			oldestKey := ""
			var oldest *modelClientEntry
			for k, v := range p.entries {
				if v.users == 0 && (oldest == nil || v.last.Before(oldest.last)) {
					oldestKey, oldest = k, v
				}
			}
			if oldest == nil {
				return nil, nil, errors.New("model client capacity reached; retry later")
			}
			if oldest.timer != nil {
				oldest.timer.Stop()
			}
			delete(p.entries, oldestKey)
		}
		e = &modelClientEntry{client: create()}
		p.entries[identity] = e
	}
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	e.users++
	e.last = time.Now()
	release := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		e.users--
		e.last = time.Now()
		if e.users == 0 {
			// Compare age as well as identity, since an already-fired old timer may
			// race with a new borrow/release of this same entry.
			idleSince := e.last
			e.timer = time.AfterFunc(ttl, func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.entries[identity] == e && e.users == 0 && !e.last.After(idleSince) {
					delete(p.entries, identity)
				}
			})
		}
	}
	return e.client, release, nil
}

type pooledModelClient struct {
	pool     *modelClientPool
	identity string
	create   func() llm.Client
}

func (c *pooledModelClient) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	client, release, err := c.pool.acquire(c.identity, c.create)
	if err != nil {
		return llm.Response{}, err
	}
	defer release()
	return client.Chat(ctx, req)
}
func (c *pooledModelClient) ChatStream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	client, release, err := c.pool.acquire(c.identity, c.create)
	if err != nil {
		return llm.Response{}, err
	}
	defer release()
	if stream, ok := client.(llm.StreamingClient); ok {
		return stream.ChatStream(ctx, req, onDelta)
	}
	return client.Chat(ctx, req)
}

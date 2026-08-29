package llm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type slowClient struct {
	inFlight int32
	peak     int32
	mu       sync.Mutex
}

func (c *slowClient) Chat(_ context.Context, _ Request) (Response, error) {
	n := atomic.AddInt32(&c.inFlight, 1)
	c.mu.Lock()
	if n > c.peak {
		c.peak = n
	}
	c.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	atomic.AddInt32(&c.inFlight, -1)
	return Response{Content: "ok"}, nil
}

// TestLimiterCapsConcurrency: parallel model calls must never exceed the cap —
// this is what keeps a parallel pipeline from tripping a per-account rate limit.
func TestLimiterCapsConcurrency(t *testing.T) {
	inner := &slowClient{}
	l := NewLimiter(inner, 2, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = l.Chat(context.Background(), Request{}) }()
	}
	wg.Wait()
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.peak > 2 {
		t.Fatalf("peak concurrency %d exceeded the cap of 2", inner.peak)
	}
}

// TestLimiterSpacesCalls: the minimum interval must actually spread out a burst.
func TestLimiterSpacesCalls(t *testing.T) {
	l := NewLimiter(&slowClient{}, 0, 40*time.Millisecond)
	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := l.Chat(context.Background(), Request{}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// three calls spaced 40ms apart cannot finish in under ~80ms
	if el := time.Since(start); el < 80*time.Millisecond {
		t.Fatalf("burst finished in %v — spacing not applied", el)
	}
}

// TestLimiterHonoursCancellation: a cancelled run must not sit in the queue.
func TestLimiterHonoursCancellation(t *testing.T) {
	l := NewLimiter(&slowClient{}, 1, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Chat(ctx, Request{}); err == nil {
		t.Fatal("expected the cancelled context to abort the call")
	}
}

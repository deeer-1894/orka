package llm

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"
)

// limiter.go — a global cap on concurrent model calls plus a minimum spacing
// between them.
//
// Why: the pipeline deliberately runs work in parallel (double-blind extraction,
// several reports at once), which is exactly what a per-account rate limit
// punishes — a real run hit "您的账户已达到速率限制" (HTTP 429) five times. Retry
// absorbs those, but retrying a limit you keep exceeding just burns latency and
// quota. Shaping the traffic at the source is the actual fix.
//
// Tunables (env, applied at construction):
//
//	ORKA_LLM_MAX_CONCURRENCY  max in-flight model calls   (default 2, 0 = unlimited)
//	ORKA_LLM_MIN_INTERVAL_MS  min gap between call starts (default 250ms)
type Limiter struct {
	Client
	sem  chan struct{}
	mu   sync.Mutex
	next time.Time
	gap  time.Duration
}

// NewLimiter wraps c so its Chat/ChatStream respect the concurrency + spacing
// limits. Wrap OUTSIDE the retry decorator so a retry also waits its turn.
func NewLimiter(c Client, maxConcurrent int, minInterval time.Duration) *Limiter {
	l := &Limiter{Client: c, gap: minInterval}
	if maxConcurrent > 0 {
		l.sem = make(chan struct{}, maxConcurrent)
	}
	return l
}

// NewLimiterFromEnv builds a limiter from the env tunables, or returns c
// unchanged when limiting is explicitly disabled.
func NewLimiterFromEnv(c Client) Client {
	maxc := envInt("ORKA_LLM_MAX_CONCURRENCY", 2)
	gap := time.Duration(envInt("ORKA_LLM_MIN_INTERVAL_MS", 250)) * time.Millisecond
	if maxc <= 0 && gap <= 0 {
		return c
	}
	return NewLimiter(c, maxc, gap)
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// acquire blocks until this call may proceed, honouring ctx cancellation.
// It returns a release func the caller must invoke.
func (l *Limiter) acquire(ctx context.Context) (func(), error) {
	// An already-cancelled run must not start a call at all — without this, the
	// very first call skips the spacing wait and would slip through.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.sem != nil {
		select {
		case l.sem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	release := func() {
		if l.sem != nil {
			<-l.sem
		}
	}
	if l.gap > 0 {
		// Serialize the *start* of calls so bursts are spread out.
		l.mu.Lock()
		now := time.Now()
		start := l.next
		if start.Before(now) {
			start = now
		}
		l.next = start.Add(l.gap)
		l.mu.Unlock()
		if wait := time.Until(start); wait > 0 {
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
				release()
				return nil, ctx.Err()
			}
		}
	}
	return release, nil
}

func (l *Limiter) Chat(ctx context.Context, req Request) (Response, error) {
	release, err := l.acquire(ctx)
	if err != nil {
		return Response{}, err
	}
	defer release()
	return l.Client.Chat(ctx, req)
}

// ChatStream forwards to the wrapped client when it streams, holding the slot
// for the whole stream so concurrency is measured in live calls, not requests.
func (l *Limiter) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	sc, ok := l.Client.(StreamingClient)
	if !ok {
		return l.Chat(ctx, req)
	}
	release, err := l.acquire(ctx)
	if err != nil {
		return Response{}, err
	}
	defer release()
	return sc.ChatStream(ctx, req, onDelta)
}

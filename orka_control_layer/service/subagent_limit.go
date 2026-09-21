package service

import (
	"context"

	"github.com/cloudwego/eino/adk"
)

const maxRunWorkers = 3

// workerLimiter is shared by every delegate in one orchestrated run. Eino may
// execute several task calls in parallel; this wrapper keeps that fan-out
// bounded without coupling independent user runs through a global semaphore.
type workerLimiter struct{ slots chan struct{} }

func newWorkerLimiter(limit int) *workerLimiter {
	if limit <= 0 || limit > maxRunWorkers {
		limit = maxRunWorkers
	}
	return &workerLimiter{slots: make(chan struct{}, limit)}
}

func (l *workerLimiter) wrap(inner adk.Agent) adk.Agent {
	if inner == nil {
		return nil
	}
	return &limitedAgent{inner: inner, limiter: l}
}

type limitedAgent struct {
	inner   adk.Agent
	limiter *workerLimiter
}

func (a *limitedAgent) Name(ctx context.Context) string        { return a.inner.Name(ctx) }
func (a *limitedAgent) Description(ctx context.Context) string { return a.inner.Description(ctx) }

func (a *limitedAgent) Run(ctx context.Context, input *adk.AgentInput, options ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	out, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer generator.Close()
		select {
		case a.limiter.slots <- struct{}{}:
			defer func() { <-a.limiter.slots }()
		case <-ctx.Done():
			return
		}
		inner := a.inner.Run(ctx, input, options...)
		for {
			event, ok := inner.Next()
			if !ok {
				return
			}
			generator.Send(event)
		}
	}()
	return out
}

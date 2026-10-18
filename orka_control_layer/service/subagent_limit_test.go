package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
)

type blockingWorkerAgent struct {
	active  atomic.Int32
	maximum atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (*blockingWorkerAgent) Name(context.Context) string        { return "worker" }
func (*blockingWorkerAgent) Description(context.Context) string { return "worker" }
func (a *blockingWorkerAgent) Run(context.Context, *adk.AgentInput, ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	out, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		current := a.active.Add(1)
		for {
			maximum := a.maximum.Load()
			if current <= maximum || a.maximum.CompareAndSwap(maximum, current) {
				break
			}
		}
		a.started <- struct{}{}
		<-a.release
		a.active.Add(-1)
		generator.Close()
	}()
	return out
}

func TestWorkerLimiterCapsOneRunAtThreeDelegates(t *testing.T) {
	base := &blockingWorkerAgent{started: make(chan struct{}, 4), release: make(chan struct{}, 4)}
	limiter := newWorkerLimiter(99)
	var drains sync.WaitGroup
	for i := 0; i < 4; i++ {
		iterator := limiter.wrap(base).Run(context.Background(), &adk.AgentInput{})
		drains.Add(1)
		go func() {
			defer drains.Done()
			for {
				if _, ok := iterator.Next(); !ok {
					return
				}
			}
		}()
	}
	for i := 0; i < maxRunWorkers; i++ {
		select {
		case <-base.started:
		case <-time.After(time.Second):
			t.Fatal("three workers did not start")
		}
	}
	select {
	case <-base.started:
		t.Fatal("fourth worker bypassed per-run limit")
	case <-time.After(50 * time.Millisecond):
	}
	base.release <- struct{}{}
	select {
	case <-base.started:
	case <-time.After(time.Second):
		t.Fatal("queued worker did not start after a slot was released")
	}
	base.release <- struct{}{}
	base.release <- struct{}{}
	base.release <- struct{}{}
	done := make(chan struct{})
	go func() { drains.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("limited workers did not finish")
	}
	if got := base.maximum.Load(); got != maxRunWorkers {
		t.Fatalf("maximum concurrent workers = %d, want %d", got, maxRunWorkers)
	}
}

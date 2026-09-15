package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFollowupCacheJoinsTabsAndIsolatesRunOwnerConversation(t *testing.T) {
	var cache followupCache
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	generate := func(context.Context) ([]string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []string{"Next?"}, nil
	}
	key := followupKey{"alice", "conv", "run"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.get(context.Background(), key, "revision", generate)
			if err != nil || len(got) != 1 || got[0] != "Next?" {
				t.Errorf("result=%v err=%v", got, err)
			}
			if len(got) > 0 {
				got[0] = "caller mutation"
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("tabs paid %d times", calls.Load())
	}
	got, err := cache.get(context.Background(), key, "other-revision", generate)
	if err != nil || len(got) != 0 || calls.Load() != 1 {
		t.Fatal("changed profile regenerated old answer")
	}
	for _, other := range []followupKey{{"bob", "conv", "run"}, {"alice", "other", "run"}, {"alice", "conv", "other"}} {
		if _, err := cache.get(context.Background(), other, "revision", generate); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatal("cache crossed owner/conversation/run boundary")
	}
}

func TestFollowupCacheCancellationDoesNotCancelSharedGeneration(t *testing.T) {
	var cache followupCache
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	key := followupKey{"alice", "conv", "run"}
	first := make(chan error, 1)
	go func() {
		_, err := cache.get(ctx, key, "revision", func(ctx context.Context) ([]string, error) {
			close(started)
			<-release
			return []string{"Next?"}, ctx.Err()
		})
		first <- err
	}()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	got, err := cache.get(context.Background(), key, "revision", func(context.Context) ([]string, error) {
		t.Error("second tab dispatched another paid call")
		return nil, nil
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("shared result=%v err=%v", got, err)
	}
}

func TestFollowupCacheRetainsFailedPaidResult(t *testing.T) {
	var cache followupCache
	calls := 0
	for i := 0; i < 2; i++ {
		_, err := cache.get(context.Background(), followupKey{"alice", "conv", "run"}, "revision", func(context.Context) ([]string, error) {
			calls++
			return nil, errors.New("unknown provider outcome")
		})
		if err == nil {
			t.Fatal("lost cached error")
		}
	}
	if calls != 1 {
		t.Fatal("failed paid invocation dispatched again")
	}
}

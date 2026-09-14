package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestResearchAndDeliveryShareCumulativeTokenBoundary(t *testing.T) {
	// Expensive non-research work must not deny the first required source until
	// the delivery reserve starts. At the default 800k cap the boundary is 600k.
	for _, spent := range []int{399999, 400000, 500000, 599999, 600000, 600001, 800000} {
		t.Run(fmt.Sprint(spent), func(t *testing.T) {
			b := newRunBudget(100, runMaxTokens, 0)
			b.AddUsage(spent, 0)
			s := newResearchSession(nil, "", b, 0)
			if s.maxCalls != 40 || b.maxTokens != 800000 {
				t.Fatal("default hard limits changed")
			}
			calls := 0
			out, err := s.invoke(context.Background(), "fetch_url", map[string]any{"url": "https://docs.example.test/required"}, func() (string, error) { calls++; return "required official source", nil })
			limited := spent >= 600000
			if err != nil || (!limited && (calls != 1 || !strings.Contains(out, "required official source"))) || (limited && (calls != 0 || out != researchLimitNotice)) {
				t.Fatalf("spent=%d calls=%d out=%s err=%v", spent, calls, out, err)
			}
			state := &adk.ChatModelAgentState{ToolInfos: []*schema.ToolInfo{{Name: "fetch_url"}, {Name: "shell"}, {Name: "file_read"}, {Name: "search_evidence"}}, DeferredToolInfos: []*schema.ToolInfo{{Name: "web_search"}, {Name: "file_write"}}}
			applyDeliveryPhase(b, state)
			if (len(state.ToolInfos) == 3) != limited || (len(state.DeferredToolInfos) == 1) != limited {
				t.Fatalf("tool visibility disagrees with retrieval at %d: %v / %v", spent, toolInfoNames(state.ToolInfos), toolInfoNames(state.DeferredToolInfos))
			}
			if got := b.observe(nil); got != (spent >= 800000) {
				t.Fatalf("global hard limit changed at %d: hit=%v reason=%s", spent, got, b.exhausted())
			}
			if spent >= 800000 && b.exhausted() != "tokens" {
				t.Fatal("800k hard stop lost")
			}
		})
	}
}

func TestResearchReserveStillAllowsCachedAndLocalEvidence(t *testing.T) {
	b := newRunBudget(100, runMaxTokens, 0)
	b.AddUsage(599999, 0)
	s := newResearchSession(nil, "", b, 0)
	ctx := context.Background()
	args := map[string]any{"url": "https://docs.example.test/required"}
	calls := 0
	call := func() (string, error) { calls++; return "required source body", nil }
	if out, err := s.invoke(ctx, "fetch_url", args, call); err != nil || !strings.Contains(out, "required source body") {
		t.Fatalf("source unavailable before reserve: %s %v", out, err)
	}
	for _, spent := range []int{600000, 800000} {
		b.AddUsage(spent-b.totalSpentTokens(), 0)
		out, err := s.invoke(ctx, "fetch_url", args, call)
		if err != nil || calls != 1 || !strings.Contains(out, "cached evidence") {
			t.Fatalf("cached read consumed remote budget at %d: calls=%d out=%s err=%v", spent, calls, out, err)
		}
		out, err = s.invoke(ctx, "fetch_url", map[string]any{"url": "https://docs.example.test/new"}, call)
		if err != nil || calls != 1 || out != researchLimitNotice {
			t.Fatalf("new remote read escaped reserve: %s %v", out, err)
		}
		for _, tool := range []string{"file_read", "search_evidence", "shell"} {
			out, err = s.invoke(ctx, tool, nil, func() (string, error) { return "local evidence or delivery", nil })
			if err != nil || out != "local evidence or delivery" {
				t.Fatalf("retrieval gate blocked local tool %s: %s %v", tool, out, err)
			}
		}
	}
	// Local availability here only concerns the retrieval gate. The global model
	// guard still stops generation at 800k, as asserted by the boundary test.
}

func TestResearchConcurrentReservationsAtDeliveryBoundary(t *testing.T) {
	b := newRunBudget(100, runMaxTokens, 0)
	b.AddUsage(599999, 0)
	s := newResearchSession(nil, "", b, 0)
	s.calls = 39
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	admitted := make(chan string, 1)
	results := make(chan string, 8)
	var remote atomic.Int32
	for i := 0; i < 8; i++ {
		go func(i int) {
			<-start
			url := fmt.Sprintf("https://docs.example.test/parallel/%d", i)
			out, err := s.invoke(ctx, "fetch_url", map[string]any{"url": url}, func() (string, error) {
				remote.Add(1)
				admitted <- url
				select {
				case <-release:
					return "shared source body", nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			})
			if err != nil {
				out = err.Error()
			}
			results <- out
		}(i)
	}
	close(start)
	var url string
	select {
	case url = <-admitted:
	case <-ctx.Done():
		t.Fatal("no required source admitted before 75%")
	}
	// All other keys are refused while the fortieth request is still in flight.
	for i := 0; i < 7; i++ {
		select {
		case out := <-results:
			if out != researchLimitNotice {
				t.Fatal(out)
			}
		case <-ctx.Done():
			t.Fatal("parallel calls exceeded their pre-reserved allowance")
		}
	}
	cp := checkpointFrom(withResearchSession(withBudget(ctx, b), s))
	if cp.ResearchCalls != 40 || cp.SpentTokens != 599999 {
		t.Fatalf("in-flight reservation absent from checkpoint: %+v", cp)
	}
	// A restart must not reclaim an already reserved remote call, even below
	// the token reserve. Restore the same fields used by runEino.
	restoredBudget := newRunBudget(100, runMaxTokens, 0)
	restoreCheckpoint(cp, restoredBudget, nil, nil)
	restored := newResearchSession(nil, "", restoredBudget, 0)
	restored.calls = cp.ResearchCalls
	out, err := restored.invoke(ctx, "fetch_url", map[string]any{"url": "https://docs.example.test/after-restart"}, func() (string, error) {
		t.Error("checkpoint reset the 40-call allowance")
		return "unexpected request", nil
	})
	if err != nil || out != researchLimitNotice {
		t.Fatalf("restored reservation not enforced: %s %v", out, err)
	}
	b.AddUsage(1, 0)
	duplicate := make(chan string, 1)
	go func() {
		out, err := s.invoke(ctx, "fetch_url", map[string]any{"url": url}, func() (string, error) { remote.Add(1); return "unexpected second request", nil })
		if err != nil {
			out = err.Error()
		}
		duplicate <- out
	}()
	releaseOnce.Do(func() { close(release) })
	for _, ch := range []<-chan string{results, duplicate} {
		select {
		case out := <-ch:
			if !strings.Contains(out, "shared source body") {
				t.Fatal(out)
			}
		case <-ctx.Done():
			t.Fatal("coalesced request did not finish")
		}
	}
	if remote.Load() != 1 {
		t.Fatalf("duplicate remote requests: %d", remote.Load())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls != 40 || len(s.pending) != 0 || len(s.cache) != 1 {
		t.Fatalf("reservation/cache accounting: calls=%d pending=%d cached=%d", s.calls, len(s.pending), len(s.cache))
	}
}

func TestResearchCheckpointCarriesBudgetThroughDeliveryBoundary(t *testing.T) {
	b := newRunBudget(100, runMaxTokens, 0)
	b.AddUsage(500000, 0)
	original := newResearchSession(nil, "", b, 0)
	original.calls = 4
	raw, err := json.Marshal(checkpointFrom(withResearchSession(withBudget(context.Background(), b), original)))
	if err != nil {
		t.Fatal(err)
	}
	var cp runCheckpoint
	if err = json.Unmarshal(raw, &cp); err != nil {
		t.Fatal(err)
	}
	next := newRunBudget(100, runMaxTokens, 0)
	restoreCheckpoint(&cp, next, nil, nil)
	resumed := newResearchSession(nil, "", next, 0)
	resumed.calls = cp.ResearchCalls // runEino restores this count from the same checkpoint.
	next.AddUsage(99999, 0)
	calls := 0
	call := func() (string, error) { calls++; return "fifth required source", nil }
	if out, err := resumed.invoke(context.Background(), "fetch_url", map[string]any{"url": "https://docs.example.test/fifth"}, call); err != nil || !strings.Contains(out, "fifth required source") {
		t.Fatalf("resumed mandatory source blocked: %s %v", out, err)
	}
	if calls != 1 || resumed.calls != 5 || next.spentTokens() != 99999 || next.totalSpentTokens() != 599999 {
		t.Fatal("resumed accounting was reset or rebilled")
	}
	next.AddUsage(1, 0)
	out, err := resumed.invoke(context.Background(), "fetch_url", map[string]any{"url": "https://docs.example.test/sixth"}, call)
	if err != nil || out != researchLimitNotice || calls != 1 {
		t.Fatalf("carried usage missed reserve: %s %v", out, err)
	}
	next.AddUsage(200000, 0)
	if !next.observe(nil) || next.exhausted() != "tokens" {
		t.Fatal("checkpoint bypassed global 800k cap")
	}
}

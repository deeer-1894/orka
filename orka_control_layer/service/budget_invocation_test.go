package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

type budgetInvocationProvider struct {
	calls int
	run   func(context.Context, int) (llm.Response, error)
}

func (p *budgetInvocationProvider) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	p.calls++
	return p.run(ctx, p.calls)
}
func TestBudgetInvocationRetrySummaryAndUnknownAreActuallyBooked(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "fake", "root", 100000, 100000)
	p := &budgetInvocationProvider{run: func(ctx context.Context, n int) (llm.Response, error) {
		if s.Snapshot().ReservedTokens == 0 {
			t.Fatal("real client dispatched without reservation")
		}
		if n == 1 {
			return llm.Response{}, &llm.APIError{Status: 503}
		}
		return llm.Response{Usage: llm.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30}}, nil
	}}
	c := llm.NewRetry(llm.NewLimiter(llm.NewAccountedClient(llm.NewMetered(p, nil)), 1, 0), llm.RetryConfig{MaxAttempts: 2, BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond})
	ctx := llm.WithAgent(s.Context(context.Background()), "summary")
	if _, err := c.Chat(ctx, llm.Request{Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "summarize"}}, MaxTokens: 100}); err != nil {
		t.Fatal(err)
	}
	a, _ := f.Load(ctx, "fake")
	if p.calls != 2 || len(a.Entries) != 2 || a.Entries[0].Source != "summary" || a.Entries[1].Source != "summary" {
		t.Fatalf("attempts not ledgered %+v", a)
	}
	got := s.Snapshot()
	if got.UnknownCalls != 1 || got.UsedTokens != got.UnknownTokens+30 || got.ReservedTokens != 0 || s.Budget().spentTokens() != got.UsedTokens {
		t.Fatalf("actual wrapper spend %+v", got)
	}
}
func TestBudgetInvocationGUIReservesWholeBoundAndUnknownKeepsIt(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 100000, 100000)
	ctx := s.Context(context.Background())
	callCtx, finish, err := llm.BeginExternalCall(ctx, llm.ExternalCallSpec{CallID: "gui", Source: "gui", MaxSteps: 3, PromptTokens: 100, MaxCompletionTokens: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().ReservedTokens; got != 3*(100+4096) {
		t.Fatalf("GUI bound %d", got)
	}
	llm.ReportExternalUsage(callCtx, llm.Usage{PromptTokens: 20, CompletionTokens: 10})
	if err := finish(ctx, false, errors.New("connection lost")); err == nil {
		t.Fatal("lost external error")
	}
	got := s.Snapshot()
	if got.UnknownTokens != 3*(100+4096) || got.ReservedTokens != 0 {
		t.Fatalf("missing GUI usage became free %+v", got)
	}
}
func TestBudgetInvocationKnownZeroVersusMissingUsage(t *testing.T) {
	for _, known := range []bool{true, false} {
		s := budgetSession(t, &fakeLedger{}, "fake", "root", 10000, 10000)
		p := &budgetInvocationProvider{run: func(context.Context, int) (llm.Response, error) {
			return llm.Response{Usage: llm.Usage{Known: known}}, nil
		}}
		c := llm.NewAccountedClient(p)
		if _, err := c.Chat(s.Context(context.Background()), llm.Request{MaxTokens: 100}); err != nil {
			t.Fatal(err)
		}
		got := s.Snapshot()
		if known && got.UsedTokens != 0 || !known && (got.UnknownCalls != 1 || got.UsedTokens == 0) {
			t.Fatalf("known=%v %+v", known, got)
		}
	}
}

func TestUsageChatRunAccountsWithoutLegacyQuota(t *testing.T) {
	calls := 0
	svc, _ := testService(t, &budgetInvocationProvider{run: func(ctx context.Context, n int) (llm.Response, error) {
		calls++
		if !llm.HasCallAccountant(ctx) {
			t.Fatal("Run lost accounting context")
		}
		return llm.Response{Content: "done", FinishReason: "stop", Usage: llm.Usage{Known: true, PromptTokens: 12, CompletionTokens: 3}}, nil
	}})
	svc.Client = llm.NewAccounted(svc.Client)
	svc.UsageLedger = &fakeLedger{}
	svc.Run(context.Background(), ChatRunRequest{Message: "hi", UserEmail: "fake", Budget: TaskBudgetRequest{MaxTokens: 1}}, func(messages.Message) {})
	if calls == 0 {
		t.Fatal("legacy budget blocked dispatch")
	}
}

func TestBudgetCheckpointPreservesPolicyAndOriginalToolAuthorization(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 500, 1000)
	tools := []string{"code", "filesystem"}
	ctx := withBudgetRequestTools(s.Context(context.Background()), tools)
	tools[0] = "mutated"
	c := checkpointFrom(ctx)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var restored runCheckpoint
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.BudgetPolicy == nil || restored.BudgetPolicy.MaxTokens != 0 || len(restored.EnabledTools) != 2 || restored.EnabledTools[0] != "code" {
		t.Fatalf("snapshot lost policy/auth: %+v", restored)
	}
	req := ChatRunRequest{Budget: TaskBudgetRequest{MaxTokens: 999999}, EnabledTools: []string{"injected"}}
	if err := applyResumeBudget(config.AgentConfig{}, &req, &restored); err != nil {
		t.Fatal(err)
	}
	if req.Budget.MaxTokens != 0 || req.EnabledTools[0] != "code" {
		t.Fatalf("resume replaced original authority: %+v", req)
	}
}

func TestBudgetSnapshotFailurePublishesOnePartialTerminal(t *testing.T) {
	f := &fakeLedger{}
	session := budgetSession(t, f, "alice", "root", 1000, 10000)
	svc, _ := testService(t, llm.NewMock())
	rc := &agent.RunContext{Ctx: session.Context(context.Background()), Vars: map[string]any{}}
	f.mu.Lock()
	f.snapshotFail = true
	f.mu.Unlock()
	c := &collector{}
	svc.publishRunOutcome(rc.Ctx, rc, messages.Meta{RunID: "root"}, c.sink, runOutcome{status: db.RunDone})
	events := c.byType(messages.EventTask)
	if len(events) != 1 || events[0].Action != db.RunPartial {
		t.Fatalf("contradictory terminal events: %+v", events)
	}
	out := rc.Vars[varPublishedRunOutcome].(runOutcome)
	if out.status != db.RunPartial || len(out.unfinished) == 0 {
		t.Fatalf("stored outcome %+v", out)
	}
}

func TestBudgetInvocationCountsSharedStepsWithoutChargingRetriesTwice(t *testing.T) {
	s, err := NewBudgetSession(context.Background(), config.AgentConfig{}, TaskBudgetRequest{MaxSteps: 1}, &fakeLedger{}, "alice", "root")
	if err != nil {
		t.Fatal(err)
	}
	p := &budgetInvocationProvider{run: func(context.Context, int) (llm.Response, error) {
		return llm.Response{Usage: llm.Usage{Known: true}}, nil
	}}
	c := llm.NewAccounted(p)
	ctx := s.Context(context.Background())
	req := llm.Request{Messages: []llm.ChatMessage{{Role: llm.RoleUser, Content: "same cycle"}}, MaxTokens: 1}
	if _, err = c.Chat(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Chat(ctx, req); err != nil {
		t.Fatal(err)
	}
	req.Messages[0].Content = "new cycle"
	if _, err = c.Chat(ctx, req); err != nil {
		t.Fatalf("step allowance not shared: %v", err)
	}
	if p.calls != 3 {
		t.Fatalf("denied step was dispatched %d", p.calls)
	}
}

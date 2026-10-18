package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/modelprofile"
)

func TestUsageResumeDiscardsHistoricalDeadline(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "alice", "root", 100, 100)
	old := time.Now().Add(-time.Hour)
	saved := &runCheckpoint{SpentTokens: 200, BudgetPolicy: &TaskBudgetRequest{MaxTokens: 100}, BudgetSnapshot: &BudgetSnapshot{Deadline: old, UsedSteps: 20}}
	req := &ChatRunRequest{}
	if err := applyResumeBudget(config.AgentConfig{}, req, saved); err != nil {
		t.Fatal(err)
	}
	restoreCheckpoint(saved, s.Budget(), nil, nil)
	ctx, cancel := s.RunContext(context.Background())
	defer cancel()
	if _, has := ctx.Deadline(); has {
		t.Fatal("restored retired deadline")
	}
	if err := s.ReserveUsage(ctx, "next", "main", 1000); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetPreDispatchSnapshotFailureCancelsReservation(t *testing.T) {
	ledger := &fakeLedger{}
	session := budgetSession(t, ledger, "alice", "root", 10000, 10000)
	ledger.mu.Lock()
	ledger.snapshotFail = true
	ledger.mu.Unlock()
	provider := &budgetInvocationProvider{run: func(context.Context, int) (llm.Response, error) {
		t.Fatal("dispatched after failed snapshot")
		return llm.Response{}, nil
	}}
	_, err := llm.NewAccounted(provider).Chat(session.Context(context.Background()), llm.Request{MaxTokens: 100})
	if err == nil {
		t.Fatal("snapshot error lost")
	}
	got := session.Snapshot()
	if got.ReservedTokens != 0 || got.UsedTokens != 0 || got.UnknownCalls != 0 {
		t.Fatalf("proven undispatched call retained charges: %+v", got)
	}
	daily, err := session.DailySnapshot(context.Background())
	if err != nil || daily.ReservedTokens != 0 || daily.UsedTokens != 0 {
		t.Fatalf("durable cancellation missing: %+v %v", daily, err)
	}
}

type budgetWireCapture struct {
	requests []llm.Request
	sources  []string
}

func (p *budgetWireCapture) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, req)
	p.sources = append(p.sources, llm.AgentFromContext(ctx))
	return llm.Response{}, errors.New("fake provider unavailable")
}
func (p *budgetWireCapture) ChatStream(ctx context.Context, req llm.Request, _ func(string)) (llm.Response, error) {
	return p.Chat(ctx, req)
}

func TestBudgetDirectCallsPassSelectedOutputPolicyToWire(t *testing.T) {
	for _, source := range []string{"attachment-vlm", "fast-path"} {
		t.Run(source, func(t *testing.T) {
			provider := &budgetWireCapture{}
			svc, _ := testService(t, provider)
			svc.Cfg.LLM.Model = "m"
			svc.DisableFastPath = false
			ctx := svc.withSelectedModel(context.Background(), "m")
			ctx = modelprofile.WithContext(ctx, modelprofile.Snapshot{ProfileID: "selected", Model: "m", Policy: modelprofile.CallPolicy{FirstMaxTokens: 321, MaxTokens: 654, ReasoningEffort: "low"}})
			switch source {
			case "attachment-vlm":
				svc.describeImages(ctx, "hello", []string{"fake-image"})
			case "fast-path":
				rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}}
				svc.tryFastPath(ctx, rc, ChatRunRequest{Message: "什么是幂等性"}, svc.Client, "m", func(messages.Message) {})
			}
			if len(provider.requests) != 1 {
				t.Fatalf("calls=%d", len(provider.requests))
			}
			got := provider.requests[0]
			if got.MaxTokens != 321 || got.ReasoningEffort != "low" || provider.sources[0] != source {
				t.Fatalf("uncapped/mislabeled wire: %+v source=%s", got, provider.sources[0])
			}
		})
	}
}

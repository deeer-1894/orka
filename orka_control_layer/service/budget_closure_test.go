package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/modelprofile"
)

func TestBudgetResumeRetainsDeadlineAndRejectsExpiredBeforeDispatch(t *testing.T) {
	for _, mode := range []string{"saved", "expired", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			svc, _ := testService(t, llm.NewMock())
			saved := &runCheckpoint{BudgetSnapshot: &BudgetSnapshot{Deadline: time.Now().Add(time.Minute)}}
			if mode == "expired" {
				saved.BudgetSnapshot.Deadline = time.Now().Add(-time.Second)
			}
			if mode == "legacy" {
				saved.BudgetSnapshot = nil
			}
			req := ChatRunRequest{UserEmail: "alice", resumeCheckpoint: saved}
			ctx, session, cancel, err := svc.prepareRunBudget(WithExecutionIdentity(context.Background(), "resume", ""), &req)
			if mode == "expired" {
				if cancel != nil {
					cancel()
				}
				if !errors.Is(err, ErrRunBudgetExceeded) {
					t.Fatalf("expired checkpoint accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			deadline, _ := ctx.Deadline()
			if mode == "saved" && !deadline.Equal(saved.BudgetSnapshot.Deadline) {
				t.Fatalf("wall allowance reset: %v != %v", deadline, saved.BudgetSnapshot.Deadline)
			}
			next := checkpointFrom(session.Context(ctx))
			if next.BudgetSnapshot.Deadline.IsZero() || !next.BudgetSnapshot.Deadline.Equal(deadline) {
				t.Fatalf("deadline lost from next checkpoint: %+v", next)
			}
		})
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
	for _, source := range []string{"title", "run-digest", "attachment-vlm", "fast-path"} {
		t.Run(source, func(t *testing.T) {
			provider := &budgetWireCapture{}
			svc, _ := testService(t, provider)
			svc.Cfg.LLM.Model = "m"
			svc.DisableFastPath = false
			ctx := svc.withSelectedModel(context.Background(), "m")
			ctx = modelprofile.WithContext(ctx, modelprofile.Snapshot{ProfileID: "selected", Model: "m", Policy: modelprofile.CallPolicy{FirstMaxTokens: 321, MaxTokens: 654, ReasoningEffort: "low"}})
			ctx = withBudgetAuxiliary(ctx)
			switch source {
			case "title":
				svc.Msg.Store = &db.Storage{} // provider fails before any database write
				svc.titleAsync(ctx, "conversation", "hello")
				waitBudgetAuxiliary(ctx)
			case "run-digest":
				svc.summarizeFindings(ctx, svc.Client, "m", "hello", "evidence")
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

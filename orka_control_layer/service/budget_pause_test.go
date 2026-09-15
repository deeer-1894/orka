package service

import (
	"context"
	"errors"
	"testing"

	"github.com/orka-oss/orka_control_layer/checkpoint"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	corecp "github.com/orka-oss/orka_core/checkpoint"
	"github.com/orka-oss/orka_core/messages"
)

// Exercise the actual Run -> interrupt -> finish -> finalization path. Both
// HTTP budget reads and this assertion use RunBudgetSnapshot's durable view.
func TestBudgetPausedRunPublishesMatchingStatus(t *testing.T) {
	for _, kind := range []string{"confirm", "clarify"} {
		for _, snapshotFailure := range []bool{false, true} {
			name := kind
			if snapshotFailure {
				name += "/snapshot-failure"
			}
			t.Run(name, func(t *testing.T) {
				ledger := &fakeLedger{}
				provider := &budgetInvocationProvider{run: func(ctx context.Context, n int) (llm.Response, error) {
					if snapshotFailure {
						ledger.mu.Lock()
						ledger.snapshotFail = true
						ledger.mu.Unlock()
					}
					if n > 1 {
						return llm.Response{Content: "finished", FinishReason: "stop", Usage: llm.Usage{Known: true, TotalTokens: 10}}, nil
					}
					tool, args := "shell", `{"command":"placeholder"}`
					if kind == "clarify" {
						tool, args = "clarify", `{"question":"which?","options":["A","B"]}`
					}
					return llm.Response{ToolCalls: []llm.ToolCall{{ID: "pause", Name: tool, Arguments: args}}, FinishReason: "tool_calls", Usage: llm.Usage{Known: true, TotalTokens: 17948}}, nil
				}}
				svc, _ := testService(t, provider)
				svc.Cfg.Storage.BaseStoragePath = t.TempDir()
				svc.UsageLedger = ledger
				svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
					return []agent.BaseTool{gateStubTool{name: "shell"}}, nil, nil
				}
				col := &collector{}
				status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "fixture", ConversationID: "pause-test", Message: "pause", ConfirmRisky: true}, col.sink)
				want := db.RunPaused
				if snapshotFailure {
					want = db.RunPartial
				}
				if status != want {
					t.Fatalf("run status=%s, want %s", status, want)
				}
				terminals := []messages.Message{}
				for _, ev := range col.byType(messages.EventTask) {
					if ev.Action != "start" {
						terminals = append(terminals, ev)
					}
				}
				if len(terminals) != 1 || terminals[0].Action != want {
					t.Fatalf("terminal status disagrees: %+v", terminals)
				}
				if snapshotFailure {
					return
				}
				snapshot, err := svc.RunBudgetSnapshot(context.Background(), "fixture", terminals[0].Meta.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Status != status || snapshot.UsedTokens != 17948 || snapshot.ReservedTokens != 0 || snapshot.RemainingTokens != 1982052 {
					t.Fatalf("budget differs from paused run: %+v", snapshot)
				}
				resumed := &collector{}
				if kind == "confirm" {
					if !svc.ResumeConfirm(context.Background(), "pause-test", true, false, resumed.sink) {
						t.Fatal("missing saved confirmation")
					}
				} else {
					if got := svc.Run(context.Background(), ChatRunRequest{UserEmail: "fixture", ConversationID: "pause-test", Message: "A", ResumeKey: col.clarifyKey()}, resumed.sink); got != db.RunDone {
						t.Fatalf("resume status=%s", got)
					}
				}
				events := resumed.byType(messages.EventTask)
				if len(events) == 0 || events[len(events)-1].Action != db.RunDone {
					t.Fatalf("resume didn't finish: %+v", events)
				}
				next, err := svc.RunBudgetSnapshot(context.Background(), "fixture", events[len(events)-1].Meta.RunID)
				if err != nil || next.Status != db.RunDone || next.UsedTokens != 17958 {
					t.Fatalf("resume budget lost cumulative state: %+v %v", next, err)
				}
				original, err := svc.RunBudgetSnapshot(context.Background(), "fixture", snapshot.RunID)
				if err != nil || original.Status != db.RunPaused {
					t.Fatalf("resume overwrote paused record: %+v %v", original, err)
				}
			})
		}
	}
}

// A failed clarification checkpoint must not publish failed while persisting
// paused (or leave the budget running). The outcome is shared by all consumers.
type budgetFailingCheckpoint struct{ checkpoint.Store }

func (budgetFailingCheckpoint) Save(context.Context, string, *corecp.Checkpoint) error {
	return errors.New("fake checkpoint storage failure")
}
func TestBudgetClarifyCheckpointFailurePublishesMatchingStatus(t *testing.T) {
	svc, store := testService(t, llm.NewMock(llm.Response{ToolCalls: []llm.ToolCall{{ID: "clarify", Name: "clarify", Arguments: `{"question":"which?"}`}}, FinishReason: "tool_calls", Usage: llm.Usage{Known: true, TotalTokens: 20}}))
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	svc.CP = budgetFailingCheckpoint{store}
	col := &collector{}
	status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "fixture", ConversationID: "failed-checkpoint", Message: "clarify"}, col.sink)
	if status != db.RunFailed {
		t.Fatalf("checkpoint error returned %s", status)
	}
	var terminal []messages.Message
	for _, event := range col.byType(messages.EventTask) {
		if event.Action != "start" {
			terminal = append(terminal, event)
		}
	}
	if len(terminal) != 1 || terminal[0].Action != db.RunFailed {
		t.Fatalf("contradictory terminal: %+v", terminal)
	}
	snapshot, err := svc.RunBudgetSnapshot(context.Background(), "fixture", terminal[0].Meta.RunID)
	if err != nil || snapshot.Status != status || snapshot.UsedTokens != 20 {
		t.Fatalf("snapshot disagrees: %+v %v", snapshot, err)
	}
}

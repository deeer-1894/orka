package service

import (
	"context"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewUnacknowledgedSideEffect(t *testing.T) {
	f := &journalFile{Seed: []*schema.Message{schema.UserMessage("append a record")}, Messages: []*schema.Message{toolCallMsg("append-once")}}
	got := resumeMessages(f)
	for _, m := range got {
		if m.Role == schema.Tool && m.ToolCallID == "append-once" && strings.Contains(m.Content, "unknown") {
			return
		}
	}
	t.Fatalf("side-effecting call disappeared without unknown-outcome receipt: %#v", got)
}
func TestReviewPartialInheritedJournal(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "resumed", []*schema.Message{schema.UserMessage("long task"), toolCallMsg("a"), toolResultMsg("a"), toolCallMsg("b"), toolResultMsg("b")})
	b := newRunBudget(100, 1000, 0)
	b.carried = 700
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "finish report", Status: "pending"}})
	j.trackState(withPlanTracker(withBudget(context.Background(), b), p))
	j.append(schema.AssistantMessage("report still pending", nil))
	j.flush()
	(&ChatService{}).settleJournal("resumed", j, db.RunPartial)
	if loadJournal(dir, "resumed") == nil {
		t.Fatal("partial resumed attempt discarded inherited work and 700-token ledger")
	}
}
func TestReviewDelegateReplay(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "delegation", nil)
	ctx := withJournal(withToolGate(context.Background(), newToolGate()), j)
	model := &gateScriptClient{respond: func(n int, req llm.Request) llm.Response {
		switch n {
		case 0:
			return gateCall("delegate", "task", `{"subagent_type":"general-purpose","description":"read local data"}`)
		case 1:
			return gateCall("read-data", "file_read", `{"path":"data.txt"}`)
		default:
			return llm.Response{Content: "finished", FinishReason: "stop"}
		}
	}}
	ag, err := BuildEinoDeepOrchestrator(ctx, model, "main", model, "mini", "delegate work", deepTestTools(), nil, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}, Messages: []messages.Message{messages.Chat(messages.RoleUser, "read data", messages.Meta{})}}
	if err = StreamEinoRun(ctx, rc, ag, func(messages.Message) {}); err != nil {
		t.Fatal(err)
	}
	f := loadJournal(dir, "delegation")
	if f == nil {
		t.Fatal("no journal")
	}
	for _, m := range f.Messages {
		t.Logf("journal role=%s calls=%v result=%s", m.Role, m.ToolCalls, m.ToolCallID)
	}
	got := resumeMessages(f)
	outstanding := map[string]bool{}
	for _, m := range got {
		if m.Role == schema.Tool {
			if !outstanding[m.ToolCallID] {
				t.Errorf("orphan completed result %q", m.ToolCallID)
			}
			delete(outstanding, m.ToolCallID)
			continue
		}
		if len(outstanding) > 0 {
			t.Errorf("unanswered calls before %s: %v", m.Role, outstanding)
		}
		for _, tc := range m.ToolCalls {
			outstanding[tc.ID] = true
		}
	}
	if len(outstanding) > 0 {
		t.Errorf("unanswered calls: %v", outstanding)
	}
}
func TestReviewClarifyRetainsDeliveryContract(t *testing.T) {
	mock := llm.NewMock(
		gateCall("declare", "update_plan", `{"steps":[{"title":"Write report","status":"pending"}],"outputs":["report.json"]}`),
		gateCall("clarify", "clarify", `{"question":"which format?","options":["A","B"]}`),
		llm.Response{Content: "finished", FinishReason: "stop"},
	)
	svc, _ := testService(t, mock)
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	col := &collector{}
	status := svc.Run(context.Background(), ChatRunRequest{Message: "write report", ConversationID: "review-clarify", UserEmail: "fixture-user"}, col.sink)
	if status != db.RunPaused {
		t.Fatalf("expected pause, got %s", status)
	}
	key := col.clarifyKey()
	if key == "" {
		t.Fatal("no clarification key")
	}
	next := &collector{}
	status = svc.Run(context.Background(), ChatRunRequest{Message: "A", ConversationID: "review-clarify", UserEmail: "fixture-user", ResumeKey: key}, next.sink)
	if status != db.RunPartial {
		t.Fatalf("clarification reset pending plan and missing required report: final status=%s", status)
	}
}

func TestConfirmationResumeRestoresDeliveryState(t *testing.T) {
	mock := llm.NewMock(
		gateCall("declare", "update_plan", `{"steps":[{"title":"Deliver","status":"pending"}],"outputs":["report.json"]}`),
		gateCall("shell-call", "shell", `{"command":"placeholder"}`),
		gateCall("check", "check_delivery", `{}`),
		llm.Response{Content: "report missing", FinishReason: "stop"},
	)
	svc, _ := testService(t, mock)
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	if err := os.MkdirAll(filepath.Join(svc.Cfg.Storage.BaseStoragePath, "fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{gateStubTool{name: "shell"}}, nil, nil
	}
	first := &collector{}
	if got := svc.Run(context.Background(), ChatRunRequest{Message: "deliver a report", ConversationID: "confirm-state", UserEmail: "fixture", ConfirmRisky: true}, first.sink); got != db.RunPaused {
		t.Fatalf("expected pause, got %s", got)
	}
	paused, ok := loadPausedRun(svc.Cfg.Storage.BaseStoragePath, "confirm-state")
	if !ok || paused.Checkpoint == nil || len(paused.Checkpoint.Outputs) != 1 {
		t.Fatalf("missing persisted confirmation state: %+v", paused)
	}
	next := &collector{}
	if !svc.ResumeConfirm(context.Background(), "confirm-state", true, false, next.sink) {
		t.Fatal("confirmation not resumed")
	}
	for _, req := range mock.Requests {
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, `"ok":false`) && strings.Contains(m.Content, "report.json") {
				return
			}
		}
	}

	t.Fatal("resumed model did not receive independent missing-output failure")
}

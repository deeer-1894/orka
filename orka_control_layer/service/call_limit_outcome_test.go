package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestCallLimitRunOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, tool, result string
		toolErr            error
		want               string
	}{
		{name: "successful tool", tool: "file_write", result: "saved", want: db.RunPartial},
		{name: "no work", want: db.RunFailed},
		{name: "plan only", tool: "update_plan", want: db.RunFailed},
		{name: "failed tool", tool: "file_write", toolErr: errors.New("invalid path"), want: db.RunFailed},
		{name: "declined operation", tool: "file_write", result: "用户拒绝了该操作,已跳过。", want: db.RunFailed},
		{name: "error payload", tool: "file_write", result: `{"isError":true,"error":"write failed"}`, want: db.RunFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := llm.Response{FinishReason: "length"}
			responses := []llm.Response{}
			if tc.tool != "" {
				args := `{}`
				if tc.tool == "update_plan" {
					args = `{"steps":[{"title":"implement and verify","status":"active"}]}`
				}
				responses = append(responses, llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "work", Name: tc.tool, Arguments: args}}})
			}
			responses = append(responses, bad, bad, bad)
			model := llm.NewMock(responses...)
			svc, _ := testService(t, model)
			svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
				return []agent.BaseTool{retrievalFixture{"file_write", func(context.Context, map[string]any) (string, error) { return tc.result, tc.toolErr }}}, nil, nil
			}
			col := &collector{}
			got := svc.Run(context.Background(), ChatRunRequest{Message: "write and verify outputs"}, col.sink)
			if got != tc.want {
				t.Errorf("status=%s want %s", got, tc.want)
			}
			wantCalls := 2
			if tc.tool != "" {
				wantCalls++
			}
			if model.Calls() != wantCalls {
				t.Errorf("call limit retried by service: %d want %d", model.Calls(), wantCalls)
			}
			tasks := col.byType(messages.EventTask)
			if len(tasks) == 0 || tasks[len(tasks)-1].Action != tc.want {
				t.Errorf("terminal event disagrees with status: %+v", tasks)
			}
			chats := col.byType(messages.EventChat)
			if len(chats) == 0 {
				t.Fatal("no limit explanation")
			}
			text := chats[len(chats)-1].Content
			if !strings.Contains(text, "模型单次调用") || strings.Contains(text, "检查工具") {
				t.Errorf("wrong diagnosis: %s", text)
			}
			if tc.want == db.RunPartial && (!strings.Contains(text, "未完成") || !strings.Contains(text, "不代表") || !strings.Contains(text, "不会自动重试")) {
				t.Errorf("dishonest or unhelpful partial: %s", text)
			}
		})
	}
}

func TestWrappedCancellationRemainsCancelled(t *testing.T) {
	svc, _ := testService(t, llm.NewMock())
	rc := &agent.RunContext{Ctx: context.Background(), Vars: map[string]any{}}
	err := fmt.Errorf("wrapped: %w", context.Canceled)
	col := &collector{}
	svc.finish(rc.Ctx, rc, messages.Meta{}, ChatRunRequest{}, col.sink, err)
	tasks := col.byType(messages.EventTask)
	if len(tasks) != 1 || !strings.Contains(fmt.Sprint(tasks[0]), "cancelled") {
		t.Fatalf("wrapped cancellation lost: %+v", tasks)
	}
}

func TestBudgetExhaustedIncludesLastFailedCallUsage(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	b.carried = 700
	b.AddUsage(250, 60)
	if b.exhausted() != "tokens" {
		t.Fatalf("last call accounting missed: %q", b.exhausted())
	}
	timed := newRunBudget(100, 0, 0)
	timed.deadline = time.Now().Add(-time.Second)
	if timed.exhausted() != "time" {
		t.Fatal("expired deadline not recorded")
	}
}

func TestCallLimitCheckpointPreservesOutcomeAndCumulativeBudget(t *testing.T) {
	model := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "length"})
	_, limitErr := llm.NewEinoModel(model, "m").WithCallLimits(llm.CallLimits{MaxTokens: 8}).Generate(context.Background(), nil)
	if !llm.IsCallLimit(limitErr) {
		t.Fatalf("fixture did not produce typed call limit: %v", limitErr)
	}
	b := newRunBudget(100, 1000, 0)
	b.carried = 600
	b.AddUsage(300, 120)
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "run verifier", Status: "pending"}})
	d := newDeliveryTracker(t.TempDir())
	if err := d.declare([]string{"report.md"}); err != nil {
		t.Fatal(err)
	}
	ctx := withDelivery(withPlanTracker(withBudget(context.Background(), b), p), d)
	if _, err := trackToolProgress([]agent.BaseTool{retrievalFixture{"file_write", func(context.Context, map[string]any) (string, error) { return "saved", nil }}}, b)[0].Invoke(ctx, nil); err != nil {
		t.Fatal(err)
	}
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}}
	wrapped := fmt.Errorf("provider generation: %w", limitErr)
	outcome := assessRunOutcome(rc, wrapped, nil)
	if outcome.status != db.RunPartial || outcome.errorDetail != wrapped.Error() || outcome.budgetHit != "tokens" || len(outcome.unfinished) < 3 {
		t.Fatalf("inconsistent terminal accounting: %+v", outcome)
	}
	if got := assessRunOutcome(rc, wrapped, context.Canceled); got.status != db.RunFailed || got.errorDetail != "cancelled" {
		t.Fatalf("user cancellation lost precedence: %+v", got)
	}
	dir := t.TempDir()
	j := newRunJournal(dir, "call-limit", nil)
	j.trackState(ctx)
	if !j.flush() {
		t.Fatal("checkpoint write failed")
	}
	saved := loadJournal(dir, "call-limit").Checkpoint
	if saved.SpentTokens != 1020 || saved.SuccessfulTools != 1 || saved.LastCallError != wrapped.Error() {
		t.Fatalf("lost error/progress/ledger: %+v", saved)
	}
	next := newRunBudget(100, 1000, 0)
	nextPlan := &planTracker{}
	nextDelivery := newDeliveryTracker(d.root)
	restoreCheckpoint(saved, next, nextPlan, nextDelivery)
	if next.toolProgress() != 1 || next.totalSpentTokens() != 1020 || next.spentTokens() != 0 || next.exhausted() != "tokens" {
		t.Fatal("resume resets allowance or loses real progress")
	}
	resumed := &agent.RunContext{Ctx: withDelivery(withPlanTracker(withBudget(context.Background(), next), nextPlan), nextDelivery), Vars: map[string]any{}}
	if got := assessRunOutcome(resumed, wrapped, nil); got.status != db.RunPartial {
		t.Fatalf("short continuation lost inherited work: %+v", got)
	}
}

func TestOneAcknowledgedToolRetainsJournalWithoutUsage(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	tool := trackToolProgress([]agent.BaseTool{retrievalFixture{"file_write", func(context.Context, map[string]any) (string, error) { return "saved", nil }}}, b)[0]
	_, _ = tool.Invoke(context.Background(), nil)
	dir := t.TempDir()
	j := newRunJournal(dir, "one-tool", nil)
	j.trackState(withBudget(context.Background(), b))
	j.append(toolCallMsg("write"))
	j.append(toolResultMsg("write"))
	svc, _ := testService(t, llm.NewMock())
	svc.settleJournal("one-tool", j, db.RunPartial)
	if loadJournal(dir, "one-tool") == nil {
		t.Fatal("successful operation discarded because provider omitted usage and transcript was short")
	}
}

func TestLegacyCheckpointRecognizesOnlyAcknowledgedWork(t *testing.T) {
	for _, tc := range []struct{ name, tool, result, want string }{
		{"saved file", "file_write", "saved", db.RunPartial},
		{"plan claim", "update_plan", "done", db.RunFailed},
		{"unknown outcome", "file_write", "[recovery: outcome unknown] inspect first", db.RunFailed},
		{"tool error", "file_write", "tool error (file_write): failed", db.RunFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "length"})
			svc, _ := testService(t, model)
			rr := &runResume{Checkpoint: &runCheckpoint{SpentTokens: 700}, Messages: []*schema.Message{
				schema.UserMessage("create files"),
				schema.AssistantMessage("", []schema.ToolCall{{ID: "old", Function: schema.FunctionCall{Name: tc.tool, Arguments: `{}`}}}),
				{Role: schema.Tool, ToolCallID: "old", Content: tc.result},
			}}
			status := svc.Run(context.Background(), ChatRunRequest{resumeFrom: rr}, func(messages.Message) {})
			if status != tc.want {
				t.Fatalf("inherited result classified %s want %s", status, tc.want)
			}
			if model.Calls() != 2 {
				t.Fatalf("service retried model limit: %d", model.Calls())
			}
		})
	}
}

func TestDeadlineCallLimitPreservesProgressWithoutCancellingParent(t *testing.T) {
	parent := context.Background()
	_, err := llm.NewEinoModel(blockingLLM{}, "m").WithCallLimits(llm.CallLimits{Timeout: 5 * time.Millisecond}).Generate(parent, nil)
	if !llm.IsCallLimit(err) || !errors.Is(err, context.DeadlineExceeded) || parent.Err() != nil {
		t.Fatalf("bad call deadline fixture: %v", err)
	}
	b := newRunBudget(100, 1000, 0)
	tool := trackToolProgress([]agent.BaseTool{retrievalFixture{"file_write", func(context.Context, map[string]any) (string, error) { return "saved", nil }}}, b)[0]
	_, _ = tool.Invoke(parent, nil)
	rc := &agent.RunContext{Ctx: withBudget(parent, b), Vars: map[string]any{}}
	out := assessRunOutcome(rc, err, nil)
	if out.status != db.RunPartial || out.errorDetail != err.Error() || out.budgetHit != "" {
		t.Fatalf("single-call timeout confused with task budget/cancellation: %+v", out)
	}
}

func TestShellFailureReceiptsDoNotBecomeProgress(t *testing.T) {
	for _, result := range []string{
		"refused for safety: command matches a blocked dangerous pattern",
		"command timed out after 30s; partial output:\nwrote file",
		"command exited with error: exit status 1\n--- output ---\ncreated file",
	} {
		t.Run(strings.Split(result, ":")[0], func(t *testing.T) {
			b := newRunBudget(100, 1000, 0)
			wrapped := progressTool{BaseTool: retrievalFixture{"shell", func(context.Context, map[string]any) (string, error) { return result, nil }}, budget: b}
			if _, err := wrapped.Invoke(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if got := b.toolProgress(); got != 0 {
				t.Errorf("failure counted as %d successful operations", got)
			}
			if got := restoreProgressFixture(t, `{"spent_tokens":100}`, result+"\n[Produced file structure issues]\nbroken SVG"); got != 0 {
				t.Errorf("legacy failure counted as %d", got)
			}
		})
	}
}

package service

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// Signal after finish has captured its second nil cancellation snapshot, so the
// test cancels while delivery assessment is blocked, without timing sleeps.
type outcomeSnapshotContext struct {
	context.Context
	reads    atomic.Int32
	captured chan struct{}
}

func (c *outcomeSnapshotContext) Err() error {
	err := c.Context.Err()
	if c.reads.Add(1) == 2 {
		close(c.captured)
	}
	return err
}
func TestCallLimitCancellationDuringDeliveryAssessment(t *testing.T) {
	model := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "length"})
	_, limitErr := llm.NewEinoModel(model, "m").WithCallLimits(llm.CallLimits{MaxTokens: 8}).Generate(context.Background(), nil)
	if !llm.IsCallLimit(limitErr) {
		t.Fatalf("not a call limit: %v", limitErr)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &outcomeSnapshotContext{Context: parent, captured: make(chan struct{})}
	b := newRunBudget(100, 1000, 0)
	b.successfulTools = 1
	d := newDeliveryTracker(t.TempDir())
	ctx := withDelivery(withBudget(observed, b), d)
	rc := &agent.RunContext{Ctx: ctx, Vars: map[string]any{}}
	svc, _ := testService(t, llm.NewMock())
	col := &collector{}
	d.mu.Lock()
	done := make(chan struct{})
	go func() { defer close(done); svc.finish(ctx, rc, messages.Meta{}, ChatRunRequest{}, col.sink, limitErr) }()
	select {
	case <-observed.captured:
	case <-time.After(2 * time.Second):
		d.mu.Unlock()
		t.Fatal("finish did not reach cancellation snapshot")
	}
	cancel()
	d.mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("finish stuck")
	}
	status := svc.finalizeRun("", rc, 0, ChatRunRequest{}, limitErr, ctx.Err())
	tasks := col.byType(messages.EventTask)
	if len(tasks) != 1 || tasks[0].Action != status || tasks[0].Action != db.RunFailed || tasks[0].Content != "cancelled" {
		t.Fatalf("cancelled record=%s but terminal events=%+v", status, tasks)
	}
	if chats := col.byType(messages.EventChat); len(chats) != 0 {
		t.Fatalf("cancelled assessment emitted partial notice: %+v", chats)
	}
}

func restoreProgressFixture(t *testing.T, checkpointJSON, result string) int {
	t.Helper()
	var c runCheckpoint
	if err := json.Unmarshal([]byte(checkpointJSON), &c); err != nil {
		t.Fatal(err)
	}
	b := newRunBudget(100, 1000, 0)
	restoreCheckpoint(&c, b, &planTracker{}, nil)
	restoreToolProgress(b, &runResume{Checkpoint: &c, Messages: []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{{ID: "shell", Function: schema.FunctionCall{Name: "shell", Arguments: `{}`}}}),
		{Role: schema.Tool, ToolCallID: "shell", Content: result},
	}})
	return b.toolProgress()
}
func TestCheckpointZeroProgressIsAuthoritative(t *testing.T) {
	for _, result := range []string{"apparently successful output", `{"exit_code":1}` + "\n[Produced-file structure check]\ninvalid CSV"} {
		if got := restoreProgressFixture(t, `{"successful_tools":0,"spent_tokens":700}`, result); got != 0 {
			t.Errorf("explicit zero inferred as %d from %q", got, result)
		}
	}
	b := newRunBudget(100, 1000, 0)
	encoded, err := json.Marshal(checkpointFrom(withBudget(context.Background(), b)))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["successful_tools"]) != "0" {
		t.Fatalf("new checkpoint omits authoritative zero: %s", encoded)
	}
	if got := restoreProgressFixture(t, string(encoded), "apparently successful output"); got != 0 {
		t.Errorf("round-tripped current zero inferred as %d", got)
	}
}
func TestLegacyDecoratedToolErrorCannotBecomeProgress(t *testing.T) {
	for _, payload := range []string{`{"exit_code":1}`, `{"isError":true}`, `{"success":false}`, `{"error":"failed"}`} {
		result := payload + "\n[Produced-file structure check]\n{\"failures\":[\"invalid CSV\"]}"
		if got := restoreProgressFixture(t, `{"spent_tokens":700}`, result); got != 0 {
			t.Errorf("known error inferred as %d: %s", got, result)
		}
	}
	if got := restoreProgressFixture(t, `{"spent_tokens":700}`, `{"exit_code":0}`+"\n[Produced-file structure check]\ninvalid CSV"); got != 1 {
		t.Errorf("legacy successful operation lost: %d", got)
	}
}

func TestCallLimitCancellationWhilePublishingNotice(t *testing.T) {
	model := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "length"})
	_, limitErr := llm.NewEinoModel(model, "m").WithCallLimits(llm.CallLimits{MaxTokens: 8}).Generate(context.Background(), nil)
	if !llm.IsCallLimit(limitErr) {
		t.Fatalf("not a call limit: %v", limitErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newRunBudget(100, 1000, 0)
	b.successfulTools = 1
	rc := &agent.RunContext{Ctx: withBudget(ctx, b), Vars: map[string]any{}}
	svc, _ := testService(t, llm.NewMock())
	col := &collector{}
	svc.finish(ctx, rc, messages.Meta{}, ChatRunRequest{}, func(m messages.Message) {
		col.sink(m)
		if m.Type == messages.EventChat {
			cancel()
		}
	}, limitErr)
	status := svc.finalizeRun("", rc, 0, ChatRunRequest{}, limitErr, ctx.Err())
	tasks := col.byType(messages.EventTask)
	if len(tasks) != 1 || tasks[0].Action != db.RunFailed || tasks[0].Action != status || tasks[0].Content != "cancelled" {
		t.Fatalf("event/record mismatch after notice callback cancellation: %s %+v", status, tasks)
	}
}

func TestCallLimitPublishedOutcomeCannotFlipDuringFinalization(t *testing.T) {
	model := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "length"})
	_, limitErr := llm.NewEinoModel(model, "m").WithCallLimits(llm.CallLimits{MaxTokens: 8}).Generate(context.Background(), nil)
	if !llm.IsCallLimit(limitErr) {
		t.Fatalf("not a call limit: %v", limitErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := newRunBudget(100, 1000, 0)
	b.successfulTools = 1
	rc := &agent.RunContext{Ctx: withBudget(ctx, b), Vars: map[string]any{}}
	svc, _ := testService(t, llm.NewMock())
	col := &collector{}
	svc.finish(ctx, rc, messages.Meta{}, ChatRunRequest{}, func(m messages.Message) {
		col.sink(m)
		if m.Type == messages.EventTask {
			cancel()
		}
	}, limitErr)
	tasks := col.byType(messages.EventTask)
	status := svc.finalizeRun("", rc, 0, ChatRunRequest{}, limitErr, ctx.Err())
	if len(tasks) != 1 || tasks[0].Action != db.RunPartial || status != tasks[0].Action {
		t.Fatalf("already published outcome changed: tasks=%+v record=%s", tasks, status)
	}
}

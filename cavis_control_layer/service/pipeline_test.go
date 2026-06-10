package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_control_layer/llm"
	"github.com/cavis-oss/cavis_control_layer/service/middlewares"
)

type echoTool struct{ calls *int }

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string  { return "echo the given text" }
func (echoTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
}
func (e echoTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	if e.calls != nil {
		*e.calls++
	}
	return "echo:" + fmt.Sprint(args["text"]), nil
}

func newRC(send func(messages.Message)) *agent.RunContext {
	return &agent.RunContext{
		Ctx:      context.Background(),
		Messages: []messages.Message{messages.Chat(messages.RoleUser, "do it", messages.Meta{})},
		Vars:     map[string]any{},
		Send:     send,
		Meta:     messages.Meta{ConversationID: "c1", TraceID: "t1"},
	}
}

func findChat(msgs []messages.Message, role string) (messages.Message, bool) {
	for _, m := range msgs {
		if m.Type == messages.EventChat && m.Role == role {
			return m, true
		}
	}
	return messages.Message{}, false
}

// tools-mid: call -> feed back -> re-think -> produce.
func TestPipeline_ToolLoopClosure(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "1", Name: "echo", Arguments: `{"text":"hi"}`}}},
		llm.Response{Content: "final answer"},
	)
	var emitted []messages.Message
	calls := 0
	rc := newRC(func(m messages.Message) { emitted = append(emitted, m) })
	rc.Tools = []agent.BaseTool{echoTool{calls: &calls}}

	pipe := BuildPipeline(SceneSimple, PipelineDeps{LLM: mock, Model: "m"})
	if err := agent.NewDefaultRunner(pipe...).Run(rc); err != nil {
		t.Fatal(err)
	}

	if calls != 1 {
		t.Fatalf("echo invoked %d times, want 1", calls)
	}
	if mock.Calls() != 2 {
		t.Fatalf("llm called %d times, want 2 (call + re-think)", mock.Calls())
	}
	if got, _ := rc.Vars[middlewares.VarFinal].(string); got != "final answer" {
		t.Fatalf("final = %q", got)
	}
	if msg, ok := findChat(emitted, messages.RoleAssistant); !ok || msg.Content != "final answer" {
		t.Fatalf("assistant chat not emitted: %+v", emitted)
	}
	// a tool event was emitted with the result fed back
	var sawTool bool
	for _, m := range emitted {
		if m.Type == messages.EventTool {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatal("no tool event emitted")
	}
}

// clarify via tools-mid interrupts; resume continues to a final answer.
func TestPipeline_ClarifyInterruptAndResume(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "1", Name: "clarify", Arguments: `{"question":"which currency?","options":["USD","EUR"]}`}}},
		llm.Response{Content: "USD rate is 7.1"},
	)
	var emitted []messages.Message
	rc := newRC(func(m messages.Message) { emitted = append(emitted, m) })

	runner := agent.NewDefaultRunner(BuildPipeline(SceneInterrupt, PipelineDeps{LLM: mock, Model: "m"})...)

	// Run interrupts at clarify.
	if err := runner.Run(rc); err != nil {
		t.Fatal(err)
	}
	if rc.Interrupt == nil || rc.Interrupt.Clarify == nil {
		t.Fatal("expected clarify interrupt")
	}
	if rc.Interrupt.Clarify.Question != "which currency?" {
		t.Fatalf("clarify question = %q", rc.Interrupt.Clarify.Question)
	}
	if rc.Cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (tools-mid, after setup+memory)", rc.Cursor)
	}
	if _, ok := rc.Vars[middlewares.VarFinal]; ok {
		t.Fatal("should not have a final answer before resume")
	}

	// Resume with the user's answer.
	if err := runner.ResumeWithParams(rc, "cp1", "USD"); err != nil {
		t.Fatal(err)
	}
	if rc.Interrupt != nil {
		t.Fatal("interrupt should be cleared after resume")
	}
	if got, _ := rc.Vars[middlewares.VarFinal].(string); got != "USD rate is 7.1" {
		t.Fatalf("final after resume = %q", got)
	}
	if msg, ok := findChat(emitted, messages.RoleAssistant); !ok || msg.Content != "USD rate is 7.1" {
		t.Fatalf("final assistant chat not emitted: %+v", emitted)
	}
}

package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

func TestAgentFirstCallHasOutputLimit(t *testing.T) {
	client := llm.NewMock(llm.Response{Content: "ok", FinishReason: "stop"})
	ag, err := BuildEinoAgent(context.Background(), client, "m", "sys", nil, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(context.Background(), ag, "perform a complex task"); err != nil {
		t.Fatal(err)
	}
	if got := client.Requests[0].MaxTokens; got != 4096 {
		t.Fatalf("first call output cap = %d, want 4096", got)
	}
}

func TestTruncatedCallsCannotExhaustADKRetriesOrFailover(t *testing.T) {
	bad := llm.Response{Content: "incomplete", FinishReason: "length"}
	client := llm.NewMock(bad, bad, bad, bad)
	backup := llm.NewMock(llm.Response{Content: "should not run"})
	ag, err := BuildEinoAgent(context.Background(), client, "m", "sys", nil, 4, backupModel(backup, "backup", "m"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(context.Background(), ag, "hi"); err == nil {
		t.Fatal("truncated output was accepted as completed work")
	}
	if client.Calls() != 2 || backup.Calls() != 0 {
		t.Fatalf("calls = %d + backup %d; want 2 + 0", client.Calls(), backup.Calls())
	}
}

func TestTruncatedToolBatchNeverExecutes(t *testing.T) {
	calls := 0
	client := llm.NewMock(
		llm.Response{FinishReason: "length", ToolCalls: []llm.ToolCall{{ID: "bad", Name: "echo", Arguments: `{"text":"rejected action"}`}}},
		llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "good", Name: "echo", Arguments: `{"text":"accepted action"}`}}},
		llm.Response{FinishReason: "stop", Content: "done"},
	)
	ag, err := BuildEinoAgent(context.Background(), client, "m", "sys", []agent.BaseTool{echoTool{calls: &calls}}, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(context.Background(), ag, "hi"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || client.Calls() != 3 {
		t.Fatalf("executed %d tools in %d calls, want 1 in 3", calls, client.Calls())
	}
	for _, msg := range client.Requests[2].Messages {
		if msg.ToolCallID == "bad" {
			t.Fatal("discarded action entered tool history")
		}
	}
	if client.Requests[2].MaxTokens != 8192 {
		t.Fatalf("later action cap = %d", client.Requests[2].MaxTokens)
	}
}

func TestLengthRetryClearsDiscardedThinking(t *testing.T) {
	resets := 0
	ctx := agent.WithEmit(context.Background(), func(m messages.Message) {
		if m.Type == "stream" && m.Action == "reset" {
			resets++
		}
	})
	client := llm.NewMock(llm.Response{FinishReason: "length"}, llm.Response{FinishReason: "stop", Content: "ok"})
	ag, err := BuildEinoAgent(ctx, client, "m", "sys", nil, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(ctx, ag, "hi"); err != nil {
		t.Fatal(err)
	}
	if resets != 1 {
		t.Fatalf("reasoning resets=%d want 1", resets)
	}
}

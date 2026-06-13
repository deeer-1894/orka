package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/llm"
)

// Phase-1 gate: drive eino's REAL ChatModelAgent + Runner end-to-end through our
// adapters (llm.EinoModel + einoTool) using the mock LLM. Proves the full stack:
// eino runner → EinoModel.Generate → (scripted tool call) → eino ToolsNode →
// einoTool.InvokableRun → our BaseTool.Invoke → result fed back → final answer.
func TestEinoAgentDrivesAdaptersEndToEnd(t *testing.T) {
	calls := 0
	echo := echoTool{calls: &calls}

	mock := llm.NewMock(
		// 1) model asks to call our "echo" tool
		llm.Response{
			ToolCalls:    []llm.ToolCall{{ID: "1", Name: "echo", Arguments: `{"text":"hi"}`}},
			FinishReason: "tool_calls",
		},
		// 2) model produces the final answer
		llm.Response{Content: "all done", FinishReason: "stop"},
	)

	ctx := context.Background()
	ag, err := BuildEinoAgent(ctx, mock, "m", "You are a helpful assistant.", []agent.BaseTool{echo}, 8)
	if err != nil {
		t.Fatalf("build eino agent: %v", err)
	}

	final, err := RunEinoOnce(ctx, ag, "please echo hi")
	if err != nil {
		t.Fatalf("run eino agent: %v", err)
	}
	if final != "all done" {
		t.Fatalf("final answer = %q, want %q", final, "all done")
	}
	if calls != 1 {
		t.Fatalf("echo tool invoked %d times via eino ToolsNode, want 1", calls)
	}
}

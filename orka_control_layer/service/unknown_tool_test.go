package service

import (
	"context"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
)

func TestUnknownToolReturnsReceiptAndValidBatchStillRuns(t *testing.T) {
	for _, deepMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "react", true: "deep"}[deepMode], func(t *testing.T) {
			ctx := withToolGate(context.Background(), newToolGate())
			calls := 0
			model := llm.NewMock(llm.Response{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{
				{ID: "unknown", Name: "read_file", Arguments: `{"path":"report.md"}`},
				{ID: "valid", Name: "file_read", Arguments: `{"path":"report.md"}`},
			}}, llm.Response{Content: "recovered", FinishReason: "stop"})
			ts := []agent.BaseTool{retrievalFixture{"file_read", func(context.Context, map[string]any) (string, error) { calls++; return "actual report", nil }}}
			ag, err := BuildEinoAgent(ctx, model, "test", "read file", ts, 10, nil)
			if deepMode {
				ag, err = BuildEinoDeepOrchestrator(ctx, model, "test", model, "test", "read file", ts, nil, 10, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			final, err := RunEinoOnce(ctx, ag, "read report")
			if err != nil {
				t.Fatalf("unknown name aborts existing work: %v", err)
			}
			if final != "recovered" || calls != 1 {
				t.Fatalf("recovery=%q real calls=%d", final, calls)
			}
			found := false
			for _, m := range model.Requests[1].Messages {
				if m.ToolCallID == "unknown" {
					found = strings.Contains(m.Content, "tool error") && strings.Contains(m.Content, "not executed")
				}
			}
			if !found {
				t.Fatal("unknown call has no explicit failure receipt")
			}
		})
	}
}

func TestUnknownToolPreservesCancellationAndBoundsDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := unknownToolReceipt(ctx, "read_file", "secret input"); err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
	out, err := unknownToolReceipt(context.Background(), strings.Repeat("x", 10000), "secret input")
	if err != nil || len(out) > 650 || strings.Contains(out, "secret input") {
		t.Fatalf("unbounded or exposed input: %d, %v", len(out), err)
	}
}

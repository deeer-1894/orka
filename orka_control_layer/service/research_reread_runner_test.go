package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
)

// Exercise public DeepAgent/task execution, including the built-in general
// worker that reuses parent middleware and repeated invocations of one worker.
func TestDeepRunnerEvidenceFirstReadPerAgentExecution(t *testing.T) {
	for _, worker := range []string{"general-purpose", "researcher"} {
		t.Run(worker, func(t *testing.T) {
			s, path, _, read := savedReadFixture(t)
			body, _ := read()
			ctx := withToolGate(withResearchSession(context.Background(), s), newToolGate())
			args, _ := json.Marshal(map[string]any{"filename": path})
			delegateArgs, _ := json.Marshal(map[string]any{"subagent_type": worker, "description": "read and verify the saved source"})
			call := func(id, name, args string) llm.Response {
				return llm.Response{ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}, FinishReason: "tool_calls"}
			}
			done := llm.Response{Content: "done", FinishReason: "stop"}
			model := llm.NewMock(
				call("parent-first", "file_read", string(args)),
				call("parent-repeat", "file_read", string(args)),
				call("delegate-one", "task", string(delegateArgs)),
				call("worker-first", "file_read", string(args)),
				call("worker-repeat", "file_read", string(args)), done,
				call("delegate-two", "task", string(delegateArgs)),
				call("worker-next-first", "file_read", string(args)),
				call("worker-next-repeat", "file_read", string(args)), done,
				call("parent-final-repeat", "file_read", string(args)), done,
			)
			var calls atomic.Int32
			fileTool := rereadFileTool(func() (string, error) { calls.Add(1); return read() }, path)
			ag, err := BuildEinoDeepOrchestrator(ctx, model, "main", model, "mini", "Read the source and delegate verification.", []agent.BaseTool{fileTool}, []config.SubAgentConfig{{Name: "researcher", Description: "verify sources", Tools: []string{"file_read"}}}, 16, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RunEinoOnce(ctx, ag, "verify the source"); err != nil {
				t.Fatal(err)
			}
			if model.Calls() != 12 || calls.Load() != 7 {
				t.Fatalf("unexpected execution: model=%d file=%d", model.Calls(), calls.Load())
			}
			for _, tc := range []struct {
				request int
				id      string
				full    bool
			}{
				{1, "parent-first", true}, {2, "parent-repeat", false}, {4, "worker-first", true}, {5, "worker-repeat", false},
				{8, "worker-next-first", true}, {9, "worker-next-repeat", false}, {11, "parent-final-repeat", false},
			} {
				var got string
				for _, message := range model.Requests[tc.request].Messages {
					if message.ToolCallID == tc.id {
						got = message.Content
					}
				}
				if tc.full && got != body {
					t.Errorf("%s first read lost content: %.100s", tc.id, got)
				}
				if !tc.full && (!strings.Contains(got, "search_evidence") || len([]rune(got)) > 1600) {
					t.Errorf("%s repeat not bounded: %d chars", tc.id, len([]rune(got)))
				}
			}
		})
	}
}

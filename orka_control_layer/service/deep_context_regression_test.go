package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_control_layer/llm"
)

func TestDeepTaskVisibleOnFirstCall(t *testing.T) {
	ctx := withToolGate(context.Background(), newToolGate())
	model := llm.NewMock(llm.Response{Content: "done"})
	ag, err := BuildEinoDeepOrchestrator(ctx, model, "main", model, "mini", "delegate independent work", deepTestTools(), nil, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RunEinoOnce(ctx, ag, "research two topics"); err != nil {
		t.Fatal(err)
	}
	if len(model.Requests) == 0 {
		t.Fatal("no model call")
	}
	var visible []string
	for _, tool := range model.Requests[0].Tools {
		visible = append(visible, tool.Name)
		if tool.Name == "task" {
			return
		}
	}
	t.Fatalf("DeepAgent's delegation tool task is absent on first real model request; visible=%v", visible)
}

func TestDeepTaskResultPreserved(t *testing.T) {
	content := strings.Repeat("Evidence with citations and analysis. ", 600)
	input := []*schema.Message{schema.UserMessage("research")}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("delegation-%d", i)
		call := schema.AssistantMessage("", nil)
		call.ToolCalls = []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "task", Arguments: `{"subagent_type":"researcher","description":"research"}`}}}
		input = append(input, call, &schema.Message{Role: schema.Tool, ToolCallID: id, ToolName: "task", Content: content})
	}
	got := runReduction(t, input)
	for _, m := range got {
		if m.Role == schema.Tool && m.ToolCallID == "delegation-0" {
			if m.Content != content {
				t.Fatalf("delegated result reduced from %d to %d bytes despite specialist protection", len(content), len(m.Content))
			}
			return
		}
	}
	t.Fatal("delegated result missing")
}

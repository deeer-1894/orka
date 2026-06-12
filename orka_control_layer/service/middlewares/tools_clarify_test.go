package middlewares

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
)

// When a batch mixes a sibling tool call with clarify, every tool_call in the
// assistant message must still get a tool reply, or a strict provider rejects
// the dangling tool_calls on resume.
func TestClarifySiblingPairingContract(t *testing.T) {
	mock := llm.NewMock(llm.Response{ToolCalls: []llm.ToolCall{
		{ID: "a", Name: "web_search", Arguments: `{"query":"x"}`},
		{ID: "b", Name: ClarifyToolName, Arguments: `{"question":"which one?"}`},
	}})
	m := &Tools{LLM: mock, Model: "m"}
	rc := &agent.RunContext{Ctx: context.Background(), Vars: map[string]any{}, Meta: messages.Meta{}, Send: func(messages.Message) {}}

	if err := m.Handle(rc, func(*agent.RunContext) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if rc.Interrupt == nil || rc.Interrupt.Clarify == nil {
		t.Fatal("expected a clarify interrupt")
	}

	hist := getHistory(rc)
	replied := map[string]bool{}
	var lastAssistantCalls int
	for _, msg := range hist {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			lastAssistantCalls = len(msg.ToolCalls)
		}
		if msg.Role == llm.RoleTool {
			replied[msg.ToolCallID] = true
		}
	}
	if lastAssistantCalls != 2 {
		t.Fatalf("assistant should carry 2 tool_calls, got %d", lastAssistantCalls)
	}
	if !replied["a"] || !replied["b"] {
		t.Errorf("every tool_call needs a reply; got %v", replied)
	}
}

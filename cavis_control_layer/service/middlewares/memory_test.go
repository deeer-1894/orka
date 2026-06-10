package middlewares

import (
	"testing"

	"github.com/cavis-oss/cavis_control_layer/llm"
)

func TestTrimHistoryKeepsSystemAndTail(t *testing.T) {
	hist := []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "1"},
		{Role: llm.RoleAssistant, Content: "2"},
		{Role: llm.RoleUser, Content: "3"},
		{Role: llm.RoleAssistant, Content: "4"},
		{Role: llm.RoleUser, Content: "5"},
	}
	out, changed := trimHistory(hist, 3)
	if !changed {
		t.Fatal("should have trimmed")
	}
	if out[0].Role != llm.RoleSystem {
		t.Error("system prompt must be preserved at head")
	}
	if out[len(out)-1].Content != "5" {
		t.Error("most recent message must be kept")
	}
	if len(out) > 3 {
		t.Errorf("expected <=3, got %d", len(out))
	}
}

func TestTrimHistoryNoOrphanToolReply(t *testing.T) {
	// If a naive trim started the tail at the RoleTool message, providers would
	// reject it (tool reply with no preceding assistant tool_calls).
	hist := []llm.ChatMessage{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "x", Name: "t"}}},
		{Role: llm.RoleTool, ToolCallID: "x", Name: "t", Content: "result"},
		{Role: llm.RoleAssistant, Content: "final"},
	}
	out, _ := trimHistory(hist, 2) // would naively start at the tool reply
	for i, m := range out {
		if i == 0 {
			continue // system
		}
		if m.Role == llm.RoleTool {
			// a kept tool reply must have its assistant tool_calls right before it
			if i == 1 || out[i-1].Role != llm.RoleAssistant || len(out[i-1].ToolCalls) == 0 {
				t.Errorf("orphan tool reply at %d", i)
			}
		}
	}
}

func TestTrimHistoryNoop(t *testing.T) {
	hist := []llm.ChatMessage{{Role: llm.RoleSystem}, {Role: llm.RoleUser}}
	if _, changed := trimHistory(hist, 50); changed {
		t.Error("should not trim when under max")
	}
}

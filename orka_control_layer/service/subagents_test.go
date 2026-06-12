package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

type namedTool string

func (n namedTool) Name() string                                         { return string(n) }
func (namedTool) Description() string                                    { return "" }
func (namedTool) Schema() map[string]any                                 { return map[string]any{} }
func (namedTool) Invoke(context.Context, map[string]any) (string, error) { return "", nil }

func toolset(names ...string) []agent.BaseTool {
	var out []agent.BaseTool
	for _, n := range names {
		out = append(out, namedTool(n))
	}
	return out
}

func names(agents []agent.BaseTool) []string {
	var out []string
	for _, a := range agents {
		out = append(out, a.Name())
	}
	return out
}

// Empty specs fall back to the built-in registry, and an agent whose tools are
// all unavailable (browser needs run_agent) is not exposed.
func TestBuildSubAgentsDefaultsAndDeadAgentSkip(t *testing.T) {
	// No run_agent available → browser must be skipped; researcher/writer remain.
	available := toolset("web_search", "fetch_url", "http_request", "current_time", "file_write", "file_read", "file_list")
	got := names(BuildSubAgents(available, nil, nil, "main-model", "mini-model", nil, nil))

	want := map[string]bool{"researcher": true, "writer": true}
	if len(got) != 2 {
		t.Fatalf("expected 2 agents (researcher, writer), got %v", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("unexpected agent %q in %v", n, got)
		}
	}
}

// A config-supplied registry overrides the defaults, scopes tools, and skips a
// spec whose declared tools are entirely unavailable.
func TestBuildSubAgentsConfigDriven(t *testing.T) {
	available := toolset("web_search", "fetch_url", "calculator")
	specs := []config.SubAgentConfig{
		{Name: "analyst", Description: "d", Tools: []string{"web_search", "calculator"}, Model: "main", MaxIters: 5},
		{Name: "ghost", Description: "d", Tools: []string{"nonexistent_tool"}}, // all tools missing → skipped
		{Name: "", Description: "d", Tools: []string{"web_search"}},            // unnamed → skipped
	}
	got := names(BuildSubAgents(available, nil, nil, "main-model", "mini-model", nil, specs))
	if len(got) != 1 || got[0] != "analyst" {
		t.Fatalf("expected only [analyst], got %v", got)
	}
}

// Integration: a real sub-agent built by BuildSubAgents runs its own scripted
// tool loop, bubbles its internal tool events into the PARENT sink TAGGED with
// agent_id (this is what the UI groups into a lane and what survives reload),
// and returns its final answer as the structured delegation result.
func TestSubAgentBubblesTaggedEventsAndReturnsFinal(t *testing.T) {
	// Researcher script: call web_search, then answer.
	mock := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "1", Name: "web_search", Arguments: `{"query":"rust web frameworks"}`}}, FinishReason: "tool_calls"},
		llm.Response{Content: "FOUND: axum and actix lead in 2025.", FinishReason: "stop"},
	)
	agents := BuildSubAgents(toolset("web_search", "fetch_url"), mock, mock, "main", "mini", nil, nil)
	var researcher agent.BaseTool
	for _, a := range agents {
		if a.Name() == "researcher" {
			researcher = a
		}
	}
	if researcher == nil {
		t.Fatal("researcher sub-agent not built")
	}

	parentMeta := messages.Meta{ConversationID: "c1", AgentID: ""} // orchestrator
	var emitted []messages.Message
	ctx := agent.WithEmit(agent.WithMeta(context.Background(), parentMeta),
		func(m messages.Message) { emitted = append(emitted, m) })

	out, err := researcher.Invoke(ctx, map[string]any{"task": "research rust web frameworks"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "FOUND: axum and actix lead in 2025." {
		t.Fatalf("delegation result = %q", out)
	}

	var taggedTool bool
	for _, m := range emitted {
		if m.Type == messages.EventTool && m.Meta.AgentID == "researcher" {
			taggedTool = true
		}
	}
	if !taggedTool {
		t.Fatalf("expected a tool event tagged agent_id=researcher in %d emitted events", len(emitted))
	}
}

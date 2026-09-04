package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
)

func deepTestTools() []agent.BaseTool {
	return []agent.BaseTool{
		gateStubTool{name: "web_search"}, gateStubTool{name: "fetch_url"},
		gateStubTool{name: "file_read"}, gateStubTool{name: "file_write"},
		gateStubTool{name: "file_list"}, gateStubTool{name: "shell"},
		gateStubTool{name: "http_request"}, gateStubTool{name: "current_time"},
	}
}

// The delegates have to arrive as AGENTS, not as one tool each: DeepAgent puts a
// single `task` tool in front of them, and that indirection is the whole point.
// One tool per specialist is what left "read that file and check X" with nothing
// to delegate to, and the orchestrator doing 120 of its 124 calls itself.
func TestSubAgentsBuildAsAgents(t *testing.T) {
	subs, err := BuildEinoSubAgents(context.Background(), nil, "main", nil, "mini", deepTestTools(), nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(subs) == 0 {
		t.Fatal("no delegates built; the task tool would have nothing to dispatch to")
	}
	seen := map[string]bool{}
	for _, s := range subs {
		seen[s.Name(context.Background())] = true
	}
	// The general workers must survive the scoping filter with these tools.
	for _, want := range []string{"researcher", "writer"} {
		if !seen[want] {
			t.Errorf("delegate %q missing from the registry", want)
		}
	}
	// And the tool-shaped view still works, for the AgentTool orchestrator.
	tools, err := BuildEinoSubAgentTools(context.Background(), nil, "main", nil, "mini", deepTestTools(), nil)
	if err != nil {
		t.Fatalf("tool view: %v", err)
	}
	if len(tools) != len(subs) {
		t.Errorf("tool view has %d entries against %d agents", len(tools), len(subs))
	}
}

// Constructing the orchestrator is the part that can fail silently — a bad
// config yields an error the caller turns into "no multi-agent" rather than a
// crash, and the run then behaves like a plain agent for reasons nobody sees.
func TestDeepOrchestratorBuilds(t *testing.T) {
	ag, err := BuildEinoDeepOrchestrator(context.Background(), nil, "main", nil, "mini",
		"you are a test orchestrator", deepTestTools(), nil, einoMaxIters, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if ag == nil {
		t.Fatal("nil orchestrator")
	}
	if got := ag.Name(context.Background()); got != einoOrchestratorName {
		t.Errorf("orchestrator name = %q, want %q", got, einoOrchestratorName)
	}
}

// A custom sub_agents registry has to reach DeepAgent too, or the task tool
// dispatches to the built-ins while the deployment thinks it configured its own.
func TestDeepOrchestratorHonoursCustomRegistry(t *testing.T) {
	specs := []config.SubAgentConfig{
		{Name: "analyst", Description: "d", Tools: []string{"file_read"}, Model: "mini"},
	}
	subs, err := BuildEinoSubAgents(context.Background(), nil, "main", nil, "mini", deepTestTools(), specs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(subs) != 1 || subs[0].Name(context.Background()) != "analyst" {
		t.Fatalf("custom registry ignored: %d delegates", len(subs))
	}
	if _, err := BuildEinoDeepOrchestrator(context.Background(), nil, "main", nil, "mini",
		"i", deepTestTools(), specs, einoMaxIters, false); err != nil {
		t.Fatalf("build with custom registry: %v", err)
	}
}

// A delegate whose tools are all unavailable must be dropped rather than
// advertised: the task tool would otherwise offer a worker that cannot act.
func TestDeepOrchestratorDropsUnbackedDelegates(t *testing.T) {
	specs := []config.SubAgentConfig{
		{Name: "ghost", Description: "d", Tools: []string{"nonexistent_tool"}, Model: "mini"},
		{Name: "real", Description: "d", Tools: []string{"file_read"}, Model: "mini"},
	}
	subs, err := BuildEinoSubAgents(context.Background(), nil, "main", nil, "mini", deepTestTools(), specs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(subs) != 1 || subs[0].Name(context.Background()) != "real" {
		t.Fatalf("a delegate with no available tools was exposed: %d built", len(subs))
	}
}

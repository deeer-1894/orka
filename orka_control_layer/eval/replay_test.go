package eval

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/llm"
)

type echoTool struct{}

func (echoTool) Name() string             { return "echo" }
func (echoTool) Description() string       { return "echo" }
func (echoTool) Schema() map[string]any    { return map[string]any{"type": "object"} }
func (echoTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	return "ok", nil
}

func sample() Sample {
	return Sample{
		Name:        "echo-then-finish",
		UserMessage: "do it",
		Script: []llm.Response{
			{ToolCalls: []llm.ToolCall{{ID: "1", Name: "echo", Arguments: `{"text":"hi"}`}}},
			{Content: "all done"},
		},
		Tools:         []agent.BaseTool{echoTool{}},
		ExpectedTools: []string{"echo"},
		ExpectedFinal: "all done",
	}
}

func TestReplay(t *testing.T) {
	s := sample()
	if err := s.Check(Replay(s, "adk")); err != nil {
		t.Fatalf("adk replay: %v", err)
	}
}

func TestReplay_DualModeReproducible(t *testing.T) {
	s := sample()
	adk := Replay(s, "adk")
	graph := Replay(s, "graph")
	if !equalStrings(adk.Tools, graph.Tools) || adk.Final != graph.Final {
		t.Fatalf("adk vs graph diverged: adk=%+v graph=%+v", adk, graph)
	}
	if err := s.Check(graph); err != nil {
		t.Fatalf("graph replay: %v", err)
	}
}

func TestReplay_DetectsDrift(t *testing.T) {
	s := sample()
	r := Replay(s, "adk")
	// golden says no tools were expected -> the real run used "echo" -> drift.
	bad := s
	bad.ExpectedTools = []string{}
	if err := bad.Check(r); err == nil {
		t.Fatal("expected drift to be detected")
	}
}

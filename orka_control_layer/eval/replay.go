// Package eval is a regression harness: it replays a recorded sample (user
// message + scripted LLM responses) through the real pipeline and compares the
// resulting tool-call sequence and final answer against a golden expectation.
// Used to catch behavioral drift and to prove adk/graph mode reproducibility.
package eval

import (
	"context"
	"fmt"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service"
)

// Sample is one recorded interaction to replay.
type Sample struct {
	Name          string
	UserMessage   string
	Script        []llm.Response  // scripted LLM responses, in order
	Tools         []agent.BaseTool
	ExpectedTools []string // expected tool-call sequence
	ExpectedFinal string   // expected final assistant content
}

// Result is what actually happened on replay.
type Result struct {
	Tools []string
	Final string
}

// Replay runs the sample through the pipeline in the given mode ("adk"/"graph").
func Replay(s Sample, mode string) Result {
	mock := llm.NewMock(s.Script...)
	var emitted []messages.Message
	rc := &agent.RunContext{
		Ctx:      context.Background(),
		Messages: []messages.Message{messages.Chat(messages.RoleUser, s.UserMessage, messages.Meta{})},
		Vars:     map[string]any{},
		Tools:    s.Tools,
		Send:     func(m messages.Message) { emitted = append(emitted, m) },
	}
	pipe := service.BuildPipeline(service.SceneSimple, service.PipelineDeps{LLM: mock, Model: "m"})
	_ = service.RunnerForMode(mode, pipe...).Run(rc)

	var res Result
	for _, m := range emitted {
		switch m.Type {
		case messages.EventTool:
			if p, ok := m.Payload.(map[string]any); ok {
				if name, ok := p["tool"].(string); ok {
					res.Tools = append(res.Tools, name)
				}
			}
		case messages.EventChat:
			if m.Role == messages.RoleAssistant {
				res.Final = m.Content
			}
		}
	}
	return res
}

// Check compares a result against the sample's golden expectation.
func (s Sample) Check(r Result) error {
	if !equalStrings(r.Tools, s.ExpectedTools) {
		return fmt.Errorf("%s: tool sequence drift: got %v want %v", s.Name, r.Tools, s.ExpectedTools)
	}
	if r.Final != s.ExpectedFinal {
		return fmt.Errorf("%s: final drift: got %q want %q", s.Name, r.Final, s.ExpectedFinal)
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

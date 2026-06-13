package service

import (
	"context"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// clarifyTool is the eino-path equivalent of the hand-rolled clarify mechanism.
// On the legacy runner clarify is a special spec the tools-mid intercepts; on
// eino it must be a real tool. Combined with ToolsConfig.ReturnDirectly the
// agent stops the moment it calls clarify, and StreamEinoRun turns that into an
// rc.Interrupt so the EXISTING checkpoint/resume machinery handles the pause —
// no eino interrupt/checkpoint-store plumbing needed (a stateless eino agent
// resumes simply by re-running over the augmented history).
type clarifyTool struct{}

func (clarifyTool) Name() string { return middlewares.ClarifyToolName }
func (clarifyTool) Description() string {
	return "Ask the user a concise clarifying question when the request is ambiguous or missing information. Calling this pauses the run until the user replies."
}
func (clarifyTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "the question to ask"},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional choices"},
		},
		"required": []string{"question"},
	}
}

// Invoke returns a short acknowledgement; the actual question/options are read
// from the recorded tool-call args by StreamEinoRun.
func (clarifyTool) Invoke(_ context.Context, _ map[string]any) (string, error) {
	return "已向用户提出澄清问题,等待回复。", nil
}

// clarifyFromArgs builds a ClarifyMessage from a clarify tool call's arguments.
func clarifyFromArgs(args map[string]any) messages.ClarifyMessage {
	c := messages.ClarifyMessage{}
	if q, ok := args["question"].(string); ok {
		c.Question = q
	}
	if raw, ok := args["options"]; ok {
		switch v := raw.(type) {
		case []string:
			c.Options = v
		case []any:
			for _, o := range v {
				if s, ok := o.(string); ok {
					c.Options = append(c.Options, s)
				}
			}
		}
	}
	return c
}

// withClarify appends the clarify tool to a tool set (for the main agent /
// orchestrator only — sub-agents return NEED_USER_INPUT instead).
func withClarify(tools []agent.BaseTool) []agent.BaseTool {
	return append(tools, clarifyTool{})
}

// clarifyReturnDirectly is the ReturnDirectly map that makes a clarify call stop
// the agent immediately so we can surface the question.
func clarifyReturnDirectly() map[string]bool {
	return map[string]bool{middlewares.ClarifyToolName: true}
}

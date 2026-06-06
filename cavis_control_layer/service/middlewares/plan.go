package middlewares

import (
	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_control_layer/llm"
)

// Plan asks the model for a short step plan and stashes it in Vars[VarPlan] for
// the complex-task scene. It emits an agent-process event so the UI can show it.
type Plan struct {
	LLM   llm.Client
	Model string
}

func (m *Plan) Name() string { return "plan-mid" }

func (m *Plan) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	if _, ok := rc.Vars[VarPlan]; ok {
		return nil // already planned (e.g. resume)
	}
	hist := getHistory(rc)
	req := llm.Request{
		Model: m.Model,
		Messages: append(append([]llm.ChatMessage{}, hist...), llm.ChatMessage{
			Role:    llm.RoleUser,
			Content: "Briefly outline a numbered step plan to accomplish the request above. Plan only; do not execute.",
		}),
	}
	resp, err := m.LLM.Chat(rc.Ctx, req)
	if err != nil {
		return nil // planning is best-effort; don't fail the run
	}
	rc.Vars[VarPlan] = resp.Content
	planMsg := messages.New(messages.EventAgent, messages.RoleAssistant, rc.Meta)
	planMsg.Action = "plan"
	planMsg.Content = resp.Content
	rc.Emit(planMsg)
	return nil
}

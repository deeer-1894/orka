package middlewares

import (
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
)

// Setup initializes the LLM history: system prompt + prior conversation. On
// resume the history already exists (restored from checkpoint), so it is left
// untouched.
type Setup struct {
	SystemPrompt string
}

func (m *Setup) Name() string { return "setup-mid" }

func (m *Setup) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	if _, ok := rc.Vars[VarLLMHistory]; ok {
		return nil // resume: keep restored history
	}
	sys := m.SystemPrompt
	if sys == "" {
		sys = DefaultSystemPrompt
	}
	sys += skillsCatalog()
	hist := []llm.ChatMessage{{Role: llm.RoleSystem, Content: sys}}
	for _, msg := range rc.Messages {
		if msg.Type != messages.EventChat {
			continue
		}
		role := msg.Role
		if role != messages.RoleAssistant && role != messages.RoleUser {
			role = messages.RoleUser
		}
		hist = append(hist, llm.ChatMessage{Role: role, Content: msg.Content})
	}
	setHistory(rc, hist)
	return nil
}

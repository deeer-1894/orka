package middlewares

import (
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// Output emits the final assistant message. It does nothing when an interrupt
// is pending (the clarify question is emitted by the control layer instead).
type Output struct{}

func (m *Output) Name() string { return "output-mid" }

func (m *Output) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	if rc.Interrupt != nil {
		return nil
	}
	if final, ok := rc.Vars[VarFinal].(string); ok && final != "" {
		rc.Emit(messages.Chat(messages.RoleAssistant, final, rc.Meta))
	}
	return nil
}

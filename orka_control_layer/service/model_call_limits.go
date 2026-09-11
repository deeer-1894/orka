package service

import (
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// newAgentModel applies one policy to primary, delegated, routed and backup
// agents. Summary generation has its own limits and stays independent.
func newAgentModel(client llm.Client, modelName, agentName string) *llm.EinoModel {
	return llm.NewEinoModel(client, modelName).ForAgent(agentName).WithCallLimits(llm.CallLimits{
		FirstMaxTokens: 4096, MaxTokens: 8192, Timeout: 180 * time.Second,
		OnLengthRetry:   emitStreamReset,
		ReasoningEffort: executionReasoningEffort,
	})
}

// executionReasoningEffort is deliberately an exact capability allow-list.
// Unknown/overridden models keep their provider default rather than receiving
// a parameter inferred from a family prefix. Output/deadline limits still apply.
func executionReasoningEffort(model string) string {
	switch model {
	case "deepseek-v4-pro", "deepseek-v4-flash":
		return "low"
	default:
		return ""
	}
}

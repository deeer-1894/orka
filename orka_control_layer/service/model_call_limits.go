package service

import (
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// newAgentModel applies one policy to primary, delegated, routed and backup
// agents. Summary generation has its own limits and stays independent.
func newAgentModel(client llm.Client, modelName, agentName string) *llm.EinoModel {
	limits := llm.CallLimits{
		FirstMaxTokens: 4096, MaxTokens: 8192, Timeout: 180 * time.Second,
		OnResponseRetry: emitStreamReset,
		ReasoningEffort: executionReasoningEffort,
	}
	// GLM-5.3 spent the entire 4k first-call allowance on reasoning in a
	// measured project run. Leave room for the first executable tool action.
	switch modelName {
	case "glm-5.3":
		limits.FirstMaxTokens = 16384
		limits.MaxTokens = 16384
		limits.Timeout = 5 * time.Minute
	case "glm-5.3-flash":
		// Measured long-file generation reached the 180s deadline at the
		// provider default max reasoning. Bound thinking and allow one small
		// module to finish without expanding the whole-run token allowance.
		limits.FirstMaxTokens = 8192
		limits.Timeout = 5 * time.Minute
	}
	return llm.NewEinoModel(client, modelName).ForAgent(agentName).WithCallLimits(limits)
}

// executionReasoningEffort is deliberately an exact capability allow-list.
// Unknown/overridden models keep their provider default rather than receiving
// a parameter inferred from a family prefix. Output/deadline limits still apply.
func executionReasoningEffort(model string) string {
	switch model {
	case "deepseek-v4-pro", "deepseek-v4-flash", "glm-5.3", "glm-5.3-flash":
		return "low"
	default:
		return ""
	}
}

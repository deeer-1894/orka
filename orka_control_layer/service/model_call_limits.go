package service

import (
	"context"
	"github.com/orka-oss/orka_core/modelprofile"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

// newAgentModel applies one policy to primary, delegated, routed and backup
// agents. Summary generation has its own limits and stays independent.
func newAgentModel(client llm.Client, modelName, agentName string) *llm.EinoModel {
	return llm.NewEinoModel(client, modelName).ForAgent(agentName).WithCallLimits(agentCallLimits(modelName))
}

func agentCallLimits(modelName string) llm.CallLimits {
	// Leave output length to the provider unless the user configured a policy.
	// A transport deadline still bounds a stalled call; it is not a task quota.
	limits := llm.CallLimits{Timeout: 5 * time.Minute, OnResponseRetry: emitStreamReset, ReasoningEffort: executionReasoningEffort}
	legacy := limits
	limits.ForContext = func(ctx context.Context, requested string) llm.CallLimits {
		snapshot, ok := modelprofile.FromContext(ctx)
		if !ok || snapshot.ProfileID == "deployment" {
			return legacy
		}
		out := llm.CallLimits{Timeout: 5 * time.Minute, OnResponseRetry: emitStreamReset}
		if snapshot.Model != requested {
			return out
		}
		p := snapshot.Policy
		if p.MaxTokens > 0 {
			out.MaxTokens = p.MaxTokens
		}
		if p.FirstMaxTokens > 0 {
			out.FirstMaxTokens = p.FirstMaxTokens
		}
		if p.TimeoutSeconds > 0 {
			out.Timeout = time.Duration(p.TimeoutSeconds) * time.Second
		}
		if p.ReasoningEffort != "" {
			out.ReasoningEffort = func(string) string { return p.ReasoningEffort }
		}
		return out
	}
	return limits
}

// executionReasoningEffort is a legacy deployment compatibility policy only.
// Named profiles use explicitly configured policies instead.
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

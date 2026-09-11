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
		OnLengthRetry: emitStreamReset,
	})
}

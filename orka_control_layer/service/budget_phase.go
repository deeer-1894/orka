package service

import (
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// deliveryPhaseTools removes only optional external-reading tools once the run
// has spent three quarters of its allowance. Existing files and execution tools
// remain available so the model can verify and package work already produced.
func deliveryPhaseTools(in []*schema.ToolInfo) []*schema.ToolInfo {
	out := make([]*schema.ToolInfo, 0, len(in))
	for _, info := range in {
		if info == nil || isResearchToolName(info.Name) {
			continue
		}
		out = append(out, info)
	}
	return out
}

func applyDeliveryPhase(b *runBudget, state *adk.ChatModelAgentState) {
	if b == nil || state == nil || b.maxTokens <= 0 || b.totalSpentTokens() < b.maxTokens*3/4 {
		return
	}
	state.ToolInfos = deliveryPhaseTools(state.ToolInfos)
	state.DeferredToolInfos = deliveryPhaseTools(state.DeferredToolInfos)
}

package service

import (
	"github.com/orka-oss/orka_core/agent"
)

// mainAgentTools adds the plan owner's control tools to the run's catalog.
func mainAgentTools(base []agent.BaseTool) []agent.BaseTool {
	out := append([]agent.BaseTool(nil), base...)
	return append(out, planTool{}, clarifyTool{}, deliveryCheckTool{}, acceptanceCheckTool{}, findTools{})
}

// workerAgentTools adds progressive discovery to a delegate's scoped tools.
func workerAgentTools(base []agent.BaseTool) []agent.BaseTool {
	out := append([]agent.BaseTool(nil), base...)
	return append(out, findTools{})
}

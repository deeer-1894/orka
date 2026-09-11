package service

import (
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
)

// runAccounting is the usage and serving-model snapshot persisted at finalization.
// Keeping extraction separate from persistence also supports runs without a store.
type runAccounting struct {
	tokens, toolCalls int
	model             string
	escalated         bool
}

func (s *ChatService) runAccounting(rc *agent.RunContext, req ChatRunRequest) runAccounting {
	stats := runAccounting{
		tokens:    middlewares.RunTokens(rc),
		toolCalls: middlewares.RunTools(rc),
		model:     req.SelectedVersion,
	}
	// A budget may exist without a reporting client (legacy/test paths), so
	// availability is separate from the amount: a reported zero is still final.
	// The shared meter includes summaries, retries and failovers absent from
	// the agent's output events. Never add the event total to it a second time.
	if rc.Ctx != nil {
		if b := budgetFrom(rc.Ctx); b != nil && b.metered && b.usageRecorded() {
			stats.tokens = b.spentTokens()
		}
	}
	if stats.model == "" && s.Cfg != nil {
		stats.model = s.Cfg.LLM.Model
	}
	// With automatic routing the request no longer says which model ran.
	if mr, ok := rc.Vars[varModelRouter].(*modelRouter); ok {
		stats.model, stats.escalated = mr.chosen()
	}
	return stats
}

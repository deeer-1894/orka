package service

import (
	"context"
	"github.com/orka-oss/orka_core/messages"
)

// runCheckpoint contains runtime obligations that must survive transcript
// compression and process restarts. Usage is cumulative across manual resumes;
// each run record still accounts only for calls made in its own attempt.
type runCheckpoint struct {
	Plan          []messages.PlanStep `json:"plan,omitempty"`
	Outputs       []string            `json:"outputs,omitempty"`
	SpentTokens   int                 `json:"spent_tokens"`
	ResearchCalls int                 `json:"research_calls,omitempty"`
}

func checkpointFrom(ctx context.Context) *runCheckpoint {
	c := &runCheckpoint{Plan: planTrackerFrom(ctx).snapshot(), Outputs: deliveryFrom(ctx).snapshot(), SpentTokens: budgetFrom(ctx).totalSpentTokens()}
	if r := researchFrom(ctx); r != nil {
		r.mu.Lock()
		c.ResearchCalls = r.calls
		r.mu.Unlock()
	}
	return c
}
func restoreCheckpoint(c *runCheckpoint, b *runBudget, p *planTracker, d *deliveryTracker) {
	if c == nil {
		return
	}
	if b != nil && c.SpentTokens > 0 {
		b.carried = c.SpentTokens
	}
	p.record(c.Plan)
	// Checkpoints only contain paths validated when first declared.
	_ = d.declare(c.Outputs)
}
func (j *runJournal) trackState(ctx context.Context) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.checkpoint = func() *runCheckpoint { return checkpointFrom(ctx) }
	j.dirty = true
}

package service

import (
	"context"
	"encoding/json"
	"github.com/orka-oss/orka_core/messages"
)

// runCheckpoint contains runtime obligations that must survive transcript
// compression and process restarts. Usage is cumulative across manual resumes;
// each run record still accounts only for calls made in its own attempt.
type runCheckpoint struct {
	SuccessfulTools         int                 `json:"successful_tools"`
	successfulToolsRecorded bool                // distinguish an authoritative zero from a legacy absent field
	LastCallError           string              `json:"last_call_error,omitempty"`
	Plan                    []messages.PlanStep `json:"plan,omitempty"`
	Outputs                 []string            `json:"outputs,omitempty"`
	SpentTokens             int                 `json:"spent_tokens"`
	ResearchCalls           int                 `json:"research_calls,omitempty"`
}

// New snapshots always serialize successful_tools, including zero. Legacy
// snapshots can omit it; preserve that distinction when loading old journals.
func (c *runCheckpoint) UnmarshalJSON(data []byte) error {
	type plain runCheckpoint
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*c = runCheckpoint(decoded)
	_, c.successfulToolsRecorded = fields["successful_tools"]
	return nil
}

func checkpointFrom(ctx context.Context) *runCheckpoint {
	c := &runCheckpoint{successfulToolsRecorded: true, Plan: planTrackerFrom(ctx).snapshot(), Outputs: deliveryFrom(ctx).snapshot(), SpentTokens: budgetFrom(ctx).totalSpentTokens()}
	if b := budgetFrom(ctx); b != nil {
		b.mu.Lock()
		c.SuccessfulTools, c.LastCallError = b.successfulTools, b.lastCallError
		b.mu.Unlock()
	}
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
	if b != nil {
		b.mu.Lock()
		b.carried = max(b.carried, c.SpentTokens)
		b.successfulTools = max(b.successfulTools, c.SuccessfulTools)
		b.lastCallError = c.LastCallError
		b.mu.Unlock()
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

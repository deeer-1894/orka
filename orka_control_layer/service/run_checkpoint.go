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
	AcceptanceRunIDs []string           `json:"acceptance_run_ids,omitempty"`
	BudgetSnapshot   *BudgetSnapshot    `json:"budget_snapshot,omitempty"`
	BudgetPolicy     *TaskBudgetRequest `json:"budget_policy,omitempty"`
	EnabledTools     []string           `json:"enabled_tools"`
	toolsRecorded    bool

	SuccessfulTools         int                            `json:"successful_tools"`
	successfulToolsRecorded bool                           // distinguish an authoritative zero from a legacy absent field
	LastCallError           string                         `json:"last_call_error,omitempty"`
	Plan                    []messages.PlanStep            `json:"plan,omitempty"`
	FinalResponse           string                         `json:"final_response,omitempty"`
	Outputs                 []string                       `json:"outputs,omitempty"`
	SpentTokens             int                            `json:"spent_tokens"`
	ResearchCalls           int                            `json:"research_calls,omitempty"`
	BrowserEvidence         map[string]planBrowserEvidence `json:"browser_evidence,omitempty"`
	BrowserFailed           bool                           `json:"browser_failed,omitempty"`
	Evidence                *evidenceCheckpoint            `json:"evidence,omitempty"`
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
	_, c.toolsRecorded = fields["enabled_tools"]
	return nil
}

func checkpointFrom(ctx context.Context) *runCheckpoint {
	plan := planTrackerFrom(ctx).checkpoint()
	c := &runCheckpoint{AcceptanceRunIDs: acceptanceRunIDs(ctx), EnabledTools: budgetRequestTools(ctx), toolsRecorded: true, successfulToolsRecorded: true, Plan: plan.Steps, BrowserEvidence: plan.Browser, Outputs: deliveryFrom(ctx).snapshot(), FinalResponse: deliveryFrom(ctx).responseMode(), SpentTokens: budgetFrom(ctx).totalSpentTokens()}
	if session := BudgetSessionFrom(ctx); session != nil {
		snapshot := session.Snapshot()
		c.BudgetSnapshot = &snapshot
		policy := snapshot.Limits
		c.BudgetPolicy = &policy
	}
	if b := budgetFrom(ctx); b != nil {
		b.mu.Lock()
		c.SuccessfulTools, c.LastCallError = b.successfulTools, b.lastCallError
		b.mu.Unlock()
	}
	if r := researchFrom(ctx); r != nil {
		r.mu.Lock()
		c.ResearchCalls = r.calls
		c.Evidence = r.evidence.checkpoint()
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
		if c.BudgetSnapshot != nil {
			snapshot := c.BudgetSnapshot
			// Restore accounting and progress, never the retired task deadline.
			b.carriedSteps = max(b.carriedSteps, snapshot.UsedSteps)
			b.sharedSteps = max(b.sharedSteps, b.carriedSteps)
			b.carriedUnknownTokens = max(b.carriedUnknownTokens, saturatingUsageSum(snapshot.UnknownTokens, snapshot.ReservedTokens))
			unknownCalls := snapshot.UnknownCalls
			if snapshot.ReservedTokens > 0 {
				unknownCalls++
			}
			b.carriedUnknownCalls = max(b.carriedUnknownCalls, unknownCalls)
		}
		b.successfulTools = max(b.successfulTools, c.SuccessfulTools)
		b.lastCallError = c.LastCallError
		b.mu.Unlock()
	}
	p.restore(planCheckpoint{Steps: c.Plan, Browser: c.BrowserEvidence}, c.BrowserFailed)
	// Checkpoints only contain paths validated when first declared.
	_ = d.configure(c.Outputs, c.FinalResponse)
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

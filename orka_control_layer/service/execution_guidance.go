package service

import "fmt"

// executionGuidance is a soft scheduling policy, not a second token ledger.
// The existing shared run budget and retrieval admission remain authoritative.
const verificationGuidance = "Follow the original user requirements. For implementation or data deliverables, write and run a real task-specific verifier as soon as the first working artifact exists; derive assertions independently from the requirements, not constants copied from generated results. Check applicable business conservation, group-to-overall consistency (including period-end aggregation), and boundary cases before charts and reports. Repair failed assertions, rerun affected checks, and retain actual command results. Complete one small executable action per generation; do not bundle the entire project into one call. Reuse saved findings; reread unchanged source files only for a specific unresolved question. This guidance and check_delivery are not proof of semantic correctness; never claim verification without executed checks and their evidence. "

func executionGuidance(b *runBudget) string {
	if b == nil || b.maxTokens <= 0 {
		return verificationGuidance
	}
	spent := b.totalSpentTokens()
	remaining := b.maxTokens - spent
	if remaining < 0 {
		remaining = 0
	}
	text := verificationGuidance + fmt.Sprintf("Task token allowance: %d/%d consumed, %d remaining (including preceding attempts). ", spent, b.maxTokens, remaining)
	switch {
	case spent >= b.maxTokens*3/4:
		return text + "Prioritize delivery now: repair failing checks, finish required report/README, manifest and archive, and run check_delivery. Stop exploratory reading and optional improvements. Do not claim incomplete work is done."
	case spent >= b.maxTokens/2:
		return text + "Prioritize verification and missing deliverables. Use saved evidence; do not repeatedly reread source files. Reserve at least the last quarter for repairs and delivery."
	case spent >= b.maxTokens/4:
		return text + "Produce runnable artifacts now, alongside only essential research. Independent data/code work need not wait for the research report. Preserve budget for verification and delivery."
	default:
		return text + "Declare required file paths with update_plan.outputs. On the first turn, make a brief plan and perform one small action; do not prepare every script before the first tool call. Keep each generation focused on the next action and split large file writes across steps. Save findings incrementally and start independent data/code work early. Reserve the last quarter for verification and delivery."
	}
}

package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const researchStateTag = "orka_research_state"

// researchGuidance runs after history compression. Live evidence and plan state
// are replaced, not accumulated in history, and never inferred from summaries.
type researchGuidance struct {
	*adk.BaseChatModelAgentMiddleware
	session *researchSession
	plan    *planTracker
	budget  *runBudget
	// A delegate cannot update the parent plan, so showing it the parent's open
	// steps and required outputs makes it adopt the whole task and never return.
	delegate bool
}

const delegateScopeNotice = "You are a delegated worker. Your brief is your whole task: complete it, verify it, then return your report. The orchestrator owns the overall plan, the other deliverables and the final answer; do not take on work outside your brief, re-run finished work, or search for tools you were not given. If the brief cannot be completed with your tools, return what you did and what is blocked."

func newResearchGuidance(ctx context.Context) *researchGuidance {
	return &researchGuidance{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, session: researchFrom(ctx), plan: planTrackerFrom(ctx), budget: budgetFrom(ctx)}
}

func newDelegateResearchGuidance(ctx context.Context) *researchGuidance {
	g := newResearchGuidance(ctx)
	g.delegate = true
	return g
}

func hasResearchTool(state *adk.ChatModelAgentState) bool {
	for _, list := range [][]*schema.ToolInfo{state.ToolInfos, state.DeferredToolInfos} {
		for _, info := range list {
			if info != nil && (isResearchToolName(info.Name) || info.Name == "search_evidence") {
				return true
			}
		}
	}
	return false
}

// BeforeAgent runs once per invocation, including delegates that inherit the
// parent's context or reuse its middleware. Model turns keep this same tracker.
func (g *researchGuidance) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	return withEvidenceReadTracker(ctx), runCtx, nil
}

func (g *researchGuidance) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	kept := make([]*schema.Message, 0, len(state.Messages)+1)
	for _, m := range state.Messages {
		if m != nil && m.Extra != nil && m.Extra[researchStateTag] == true {
			continue
		}
		kept = append(kept, m)
	}
	// Remove the previous replaceable notice even when a budget notice is now
	// authoritative; stale execution instructions must not survive that boundary.
	state.Messages = kept
	budget := g.budget
	if g.session != nil {
		budget = g.session.budget
	}
	if budget.exhausted() != "" {
		return ctx, state, nil
	}
	text := "[Live execution state]\n"
	if g.delegate {
		text += delegateScopeNotice + "\n"
	}
	text += executionGuidance(budget)
	if g.session != nil && (!g.delegate || hasResearchTool(state)) {
		text += "\n" + g.session.status() + "\nFor a known documentation page URL, fetch_url directly; use discover_docs only when you need to find unknown page links from an index, and read_section for a specific unresolved question. Record version, endpoint, authentication scope and inclusive/exclusive boundaries with API claims; do not generalize them beyond the retrieved evidence. Prefer search_evidence and saved findings to rereading unchanged sources. Once the original user's evidence requirements are met, stop research and execute the next required verification or delivery step."
	}
	if g.delegate {
		text += "\nFor software/data execution report the actual verifier commands and failures; for qualitative research verify sources and dates without creating test scripts. Do not claim unverified results."
		notice := runtimeUserMessage(text)
		notice.Extra[researchStateTag] = true
		state.Messages = append(kept, notice)
		g.filterExhaustedResearch(state)
		return ctx, state, nil
	}
	text += "\nUpdate only genuinely completed plan steps; for software/data execution record actual verifier commands and failures; for qualitative research verify sources and dates without creating test scripts. Do not mark missing or unverified deliverables done."
	if outputs := deliveryFrom(ctx).snapshot(); len(outputs) > 0 {
		text += "\nRequired outputs (check_delivery inspects these): " + trunc(strings.Join(outputs, "; "), 2400)
	}
	if deliveryFrom(ctx).responseMode() == "file_receipt" {
		text += "\nFinal response mode: file_receipt. Put substantive conclusions, actual verification results and ALL unresolved limitations in the declared report. Once all required files are checked and original plan steps are genuinely complete, end with a brief confirmation; runtime will publish scoped file links and file-check results. Do not spend a generation rewriting statistics. If the user requires a separate substantive chat answer, switch update_plan.final_response back to answer."
	}
	var pending []string
	for _, step := range g.plan.snapshot() {
		if step.Status == "done" {
			continue
		}
		pending = append(pending, fmt.Sprintf("id=%q status=%s title=%q", step.ID, step.Status, step.Title))
	}
	if len(pending) > 0 {
		text += "\nOpen plan steps (reuse these exact IDs when updating; do not recreate existing obligations under new IDs):\n" + trunc(strings.Join(pending, "\n"), 2400)
	}
	notice := runtimeUserMessage(text)
	notice.Extra[researchStateTag] = true
	state.Messages = append(kept, notice)
	g.filterExhaustedResearch(state)
	return ctx, state, nil
}

func (g *researchGuidance) filterExhaustedResearch(state *adk.ChatModelAgentState) {
	limited := false
	if g.session != nil {
		g.session.mu.Lock()
		limited = g.session.atLimitLocked()
		g.session.mu.Unlock()
	}
	if limited {
		filter := func(in []*schema.ToolInfo) []*schema.ToolInfo {
			out := make([]*schema.ToolInfo, 0, len(in))
			for _, info := range in {
				if info != nil && !isResearchTool(info.Name, nil) {
					out = append(out, info)
				}
			}
			return out
		}
		state.ToolInfos = filter(state.ToolInfos)
		state.DeferredToolInfos = filter(state.DeferredToolInfos)
	}
}

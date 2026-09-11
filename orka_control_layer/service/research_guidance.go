package service

import (
	"context"
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
}

func newResearchGuidance(ctx context.Context) *researchGuidance {
	return &researchGuidance{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, session: researchFrom(ctx), plan: planTrackerFrom(ctx), budget: budgetFrom(ctx)}
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
	text := "[Live execution state]\n" + executionGuidance(budget)
	if g.session != nil {
		text += "\n" + g.session.status() + "\nFor documentation, use discover_docs to find real links and read_section for a specific unresolved question. Prefer search_evidence and saved findings to rereading unchanged sources. Once the original user's evidence requirements are met, stop research and execute the next required verification or delivery step."
	}
	text += "\nUpdate only genuinely completed plan steps; record actual verifier commands and failures. Do not mark missing or unverified deliverables done."
	if outputs := deliveryFrom(ctx).snapshot(); len(outputs) > 0 {
		text += "\nRequired outputs (check_delivery inspects these): " + trunc(strings.Join(outputs, "; "), 2400)
	}
	if pending := g.plan.unfinished(); len(pending) > 0 {
		text += "\nOpen plan steps: " + trunc(strings.Join(pending, "; "), 1800)
	}
	notice := runtimeUserMessage(text)
	notice.Extra[researchStateTag] = true
	state.Messages = append(kept, notice)
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
				if info != nil && !isResearchTool(info.Name) {
					out = append(out, info)
				}
			}
			return out
		}
		state.ToolInfos = filter(state.ToolInfos)
		state.DeferredToolInfos = filter(state.DeferredToolInfos)
	}
	return ctx, state, nil
}

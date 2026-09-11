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
}

func newResearchGuidance(ctx context.Context) *researchGuidance {
	return &researchGuidance{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, session: researchFrom(ctx), plan: planTrackerFrom(ctx)}
}

// BeforeAgent runs once per invocation, including delegates that inherit the
// parent's context or reuse its middleware. Model turns keep this same tracker.
func (g *researchGuidance) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	return withEvidenceReadTracker(ctx), runCtx, nil
}

func (g *researchGuidance) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if g.session == nil || g.session.budget.exhausted() != "" {
		return ctx, state, nil
	}
	kept := make([]*schema.Message, 0, len(state.Messages)+1)
	for _, m := range state.Messages {
		if m != nil && m.Extra != nil && m.Extra[researchStateTag] == true {
			continue
		}
		kept = append(kept, m)
	}
	text := "[Live execution state]\n" + executionGuidance(g.session.budget) + "\n" + g.session.status() + "\n" +
		"For documentation, use discover_docs to find real links and read_section for a specific question. Prefer search_evidence to repeated network reads. Save sourced findings as you collect them; once required evidence is sufficient, move to implementation and verification. Update only genuinely completed plan steps; do not declare missing deliverables done."
	if outputs := deliveryFrom(ctx).snapshot(); len(outputs) > 0 {
		text += "\nRequired outputs (check_delivery inspects these): " + trunc(strings.Join(outputs, "; "), 2400)
	}
	if pending := g.plan.unfinished(); len(pending) > 0 {
		text += "\nOpen plan steps: " + trunc(strings.Join(pending, "; "), 1800)
	}
	notice := runtimeUserMessage(text)
	notice.Extra[researchStateTag] = true
	state.Messages = append(kept, notice)
	g.session.mu.Lock()
	limited := g.session.atLimitLocked()
	g.session.mu.Unlock()
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

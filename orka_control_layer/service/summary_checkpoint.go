package service

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const workSummaryTag = "orka_work_summary"

// finalizeSummary keeps exact human requests outside the lossy work summary.
// Provenance is assigned at chat ingestion, never inferred from text or role
// alone: digests, recovery notes and live guidance also use the user role.
func finalizeSummary(_ context.Context, original []*schema.Message, summary *schema.Message) ([]*schema.Message, error) {
	if summary == nil || summary.Role != schema.Assistant || strings.TrimSpace(summary.Content) == "" || (summary.ResponseMeta != nil && summary.ResponseMeta.FinishReason == "length") {
		return nil, errors.New("incomplete summary")
	}
	out := make([]*schema.Message, 0, len(original)+1)
	var requests []*schema.Message
	for _, m := range original {
		if m == nil {
			continue
		}
		if isUnknownUserInput(m) {
			return nil, errors.New("ambiguous legacy user input; preserve original history")
		}
		if m.Role == schema.System {
			out = append(out, m)
		}
		if isHumanRequest(m) {
			requests = append(requests, m)
		}
	}
	if len(requests) == 0 {
		return nil, errors.New("no trusted user requests; preserve original history")
	}
	checkpoint := schema.AssistantMessage("[Model-written work summary; the original user requests below remain authoritative.]\n"+summary.Content, nil)
	checkpoint.Extra = map[string]any{workSummaryTag: true}
	out = append(out, checkpoint)
	return append(out, requests...), nil
}

// requestAwareSummary prevents retained requests alone from repeatedly firing
// the count/token trigger. User text is never silently trimmed to make a
// compression look successful; the normal run/provider limits still apply.
type summaryRewriter interface {
	adk.ChatModelAgentMiddleware
	Summarize(context.Context, *adk.ChatModelAgentState) ([]*schema.Message, error)
}

type requestAwareSummary struct{ summaryRewriter }

func (m *requestAwareSummary) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	var work []*schema.Message
	hasRequests := false
	for _, msg := range state.Messages {
		// A new answer does not remove ambiguity in earlier legacy messages.
		if isUnknownUserInput(msg) {
			return ctx, state, nil
		}
		hasRequests = hasRequests || isHumanRequest(msg)
		if msg == nil || msg.Role == schema.System || isHumanRequest(msg) || isRuntimeInput(msg) || msg.Extra[workSummaryTag] == true {
			continue
		}
		work = append(work, msg)
	}
	if !hasRequests || len(work) == 0 {
		return ctx, state, nil
	}
	trigger := summarizationTrigger()
	if len(work) >= trigger.ContextMessages {
		rewritten, err := m.summaryRewriter.Summarize(ctx, state)
		if err != nil {
			return ctx, state, err
		}
		next := *state
		next.Messages = rewritten
		return ctx, &next, nil
	}
	// This estimate only prevents churn when immutable text alone is too large.
	// Otherwise Eino owns the total-token decision, including provider usage and
	// tool schemas. A debug byte estimate must never suppress that decision.
	workTokens, _, _, _ := probeCount(work)
	totalTokens, _, _, _ := probeCount(state.Messages)
	if totalTokens-workTokens >= int64(trigger.ContextTokens) && workTokens < int64(trigger.ContextTokens) {
		return ctx, state, nil
	}
	return m.summaryRewriter.BeforeModelRewriteState(ctx, state, mc)
}

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestExecutionRuntimeGuidanceRestoredWithoutResearchSession(t *testing.T) {
	ctx := withBudget(context.Background(), newRunBudget(100, 1000, 0))
	g := newResearchGuidance(ctx)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("original requirements")}}
	for _, compressed := range []bool{false, false, true, false} {
		if compressed {
			state.Messages = []*schema.Message{schema.AssistantMessage("summary omitted runtime notice", nil)}
		}
		_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
		count := 0
		for _, m := range state.Messages {
			if m.Extra[researchStateTag] == true {
				count++
				if !isRuntimeInput(m) || !strings.Contains(m.Content, "write and run") || !strings.Contains(m.Content, "before charts and reports") {
					t.Errorf("lost provenance or verification guidance: %+v", m)
				}
			}
		}
		if count != 1 {
			t.Errorf("runtime notice count=%d want 1 (compression=%v)", count, compressed)
		}
	}
}
func TestExhaustionRemovesStaleExecutionInstructions(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	session := newResearchSession(nil, "", b, 10)
	ctx := withResearchSession(withBudget(context.Background(), b), session)
	g := newResearchGuidance(ctx)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("go")}}
	_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
	b.AddUsage(1000, 0)
	state.Messages = append(state.Messages, budgetNotice("tokens"))
	_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
	for _, m := range state.Messages {
		if m.Extra[researchStateTag] == true {
			t.Fatal("stale execute-tools notice contradicts exhausted budget")
		}
	}
}

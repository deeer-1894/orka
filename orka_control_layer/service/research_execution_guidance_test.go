package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_core/messages"
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
				if !isRuntimeInput(m) || !strings.Contains(m.Content, "write and run") || !strings.Contains(m.Content, "For browsing, news summaries") {
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

func TestCompressedGuidancePreservesPlanIdentity(t *testing.T) {
	tracker := &planTracker{}
	tracker.record([]messages.PlanStep{{ID: "pkg-original", Title: "package and verify", Status: "active"}})
	ctx := withBudget(context.Background(), newRunBudget(100, 1000, 0))
	g := newResearchGuidance(ctx)
	g.plan = tracker
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.AssistantMessage("compressed summary without IDs", nil)}}
	_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
	notice := state.Messages[len(state.Messages)-1].Content
	if !strings.Contains(notice, "pkg-original") || !strings.Contains(notice, "package and verify") {
		t.Fatalf("compression lost canonical plan identity: %s", notice)
	}
	tracker.record([]messages.PlanStep{{ID: "pkg-original", Title: "verify clean extraction", Status: "active"}})
	_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
	notice = state.Messages[len(state.Messages)-1].Content
	if !strings.Contains(notice, "pkg-original") || !strings.Contains(notice, "verify clean extraction") || strings.Contains(notice, "package and verify") {
		t.Fatalf("canonical ID/title update not reflected: %s", notice)
	}
}

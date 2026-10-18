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

func TestDelegateGuidanceOmitsParentPlanAndUnavailableResearch(t *testing.T) {
	tracker := &planTracker{}
	tracker.record([]messages.PlanStep{{ID: "step1", Title: "generate csv", Status: "pending"}, {ID: "step6", Title: "write report.md", Status: "pending"}})
	b := newRunBudget(100, 1000, 0)
	ctx := withResearchSession(withBudget(context.Background(), b), newResearchSession(nil, "", b, 10))
	notice := func(g *researchGuidance, tools ...string) string {
		g.plan = tracker
		state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("brief")}, ToolInfos: infos(tools...)}
		_, state, _ = g.BeforeModelRewriteState(ctx, state, nil)
		return state.Messages[len(state.Messages)-1].Content
	}
	engineer := notice(newDelegateResearchGuidance(ctx), "shell", "file_write", "file_read")
	if !strings.Contains(engineer, "delegated worker") {
		t.Errorf("delegate scope missing: %s", engineer)
	}
	for _, leaked := range []string{"step6", "write report.md", "Open plan steps", "Update only genuinely completed plan steps", "Prefer search_evidence and saved findings"} {
		if strings.Contains(engineer, leaked) {
			t.Errorf("delegate notice leaks %q: %s", leaked, engineer)
		}
	}
	if researcher := notice(newDelegateResearchGuidance(ctx), "web_search", "search_evidence"); !strings.Contains(researcher, "Prefer search_evidence") {
		t.Errorf("research delegate lost evidence guidance: %s", researcher)
	}
	orchestrator := notice(newResearchGuidance(ctx))
	if strings.Contains(orchestrator, "delegated worker") || !strings.Contains(orchestrator, "step6") || !strings.Contains(orchestrator, "Prefer search_evidence") {
		t.Errorf("orchestrator guidance changed: %s", orchestrator)
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

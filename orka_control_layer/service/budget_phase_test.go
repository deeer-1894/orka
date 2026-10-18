package service

import (
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"testing"
)

func toolInfoNames(in []*schema.ToolInfo) []string {
	out := []string{}
	for _, x := range in {
		out = append(out, x.Name)
	}
	return out
}

func TestDeliveryPhaseRemovesOnlyResearchTools(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	b.AddUsage(750, 0)
	state := &adk.ChatModelAgentState{ToolInfos: []*schema.ToolInfo{{Name: "http_request"}, {Name: "file_write"}, {Name: "search_evidence"}, {Name: "shell"}}, DeferredToolInfos: []*schema.ToolInfo{{Name: "fetch_url"}, {Name: "verify"}}}
	applyDeliveryPhase(b, state)
	if len(state.ToolInfos) != 3 || state.ToolInfos[0].Name != "file_write" || state.ToolInfos[1].Name != "search_evidence" || state.ToolInfos[2].Name != "shell" {
		t.Fatalf("tool infos=%v names=%v", state.ToolInfos, toolInfoNames(state.ToolInfos))
	}
	if len(state.DeferredToolInfos) != 1 || state.DeferredToolInfos[0].Name != "verify" {
		t.Fatalf("deferred=%v", state.DeferredToolInfos)
	}
}
func TestDeliveryPhaseDoesNotHideResearchBeforeReserve(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	b.AddUsage(749, 0)
	state := &adk.ChatModelAgentState{ToolInfos: []*schema.ToolInfo{{Name: "http_request"}, {Name: "file_write"}}}
	applyDeliveryPhase(b, state)
	if len(state.ToolInfos) != 2 {
		t.Fatalf("unexpected early filtering: %v", state.ToolInfos)
	}
}

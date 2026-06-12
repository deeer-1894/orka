package agent

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/messages"
)

// a runner that records the child RunContext it was given and writes a result.
type recRunner struct {
	gotAgentID  string
	gotParentID string
	gotConvID   string
	gotBrief    string
}

func (r *recRunner) Run(rc *RunContext) error {
	r.gotAgentID = rc.Meta.AgentID
	r.gotParentID = rc.Meta.ParentAgentID
	r.gotConvID = rc.Meta.ConversationID
	if len(rc.Messages) > 0 {
		r.gotBrief = rc.Messages[0].Content
	}
	rc.Vars["final"] = "RESULT: done"
	return nil
}
func (r *recRunner) ResumeWithParams(*RunContext, string, string) error { return nil }

func TestAgentToolIsolationAndMeta(t *testing.T) {
	rec := &recRunner{}
	at := &AgentTool{
		Spec:  AgentSpec{Name: "researcher", Description: "research"},
		Tools: nil,
		Build: func([]BaseTool) Runner { return rec },
		Final: func(rc *RunContext) string { return rc.Vars["final"].(string) },
	}

	// parent context carries the orchestrator's meta + an emit sink
	var emitted int
	parentMeta := messages.Meta{ConversationID: "c1", TraceID: "t1", AgentID: ""}
	ctx := WithEmit(WithMeta(context.Background(), parentMeta), func(messages.Message) { emitted++ })

	out, err := at.Invoke(ctx, map[string]any{"task": "find X"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "RESULT: done" {
		t.Errorf("structured result = %q", out)
	}
	if rec.gotAgentID != "researcher" {
		t.Errorf("child AgentID = %q, want researcher", rec.gotAgentID)
	}
	if rec.gotParentID != "" {
		t.Errorf("child ParentAgentID = %q, want orchestrator (empty)", rec.gotParentID)
	}
	if rec.gotConvID != "c1" {
		t.Errorf("child should inherit conversation id, got %q", rec.gotConvID)
	}
	if rec.gotBrief != "find X" {
		t.Errorf("child brief = %q", rec.gotBrief)
	}
}

func TestAgentToolEmptyBrief(t *testing.T) {
	at := &AgentTool{Spec: AgentSpec{Name: "x"}, Build: func([]BaseTool) Runner { return &recRunner{} }}
	if _, err := at.Invoke(context.Background(), map[string]any{}); err == nil {
		t.Error("empty brief should error")
	}
}

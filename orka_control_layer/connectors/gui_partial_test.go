package connectors

import (
	"strings"
	"testing"
)

// Missing receipts cannot establish that the browser was unchanged: an action
// may have taken effect before its response arrived.
func TestPartialResultWithNoSteps(t *testing.T) {
	var evidence guiEvidence
	got := evidence.result("partial", "timeout")
	if strings.Contains(got, "Nothing was changed") || !strings.Contains(got, "missing receipts") {
		t.Errorf("overclaims an unchanged page: %s", got)
	}
}

func TestDescribeStepOnlyCountsActions(t *testing.T) {
	if got := describeStep(map[string]any{"type": "action", "action": "click", "target": "登录"}); got != "click 登录" {
		t.Errorf("got %q, want \"click 登录\"", got)
	}
	// Screenshots and observations change nothing, so they must not appear in a
	// summary of what was DONE.
	for _, frame := range []map[string]any{
		{"type": "screenshot", "data": "..."},
		{"type": "observe", "mode": "grounded"},
		{"type": "action"}, // malformed: no action name
	} {
		if got := describeStep(frame); got != "" {
			t.Errorf("frame %v produced step %q, want none", frame, got)
		}
	}
}

func TestDescribeStepWithoutTarget(t *testing.T) {
	if got := describeStep(map[string]any{"type": "action", "action": "scroll"}); got != "scroll" {
		t.Errorf("got %q, want \"scroll\"", got)
	}
}

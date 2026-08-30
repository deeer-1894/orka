package connectors

import (
	"strings"
	"testing"
	"time"
)

// A GUI run that times out at step 8 of 10 really did those eight things — the
// browser state reflects them. Returning a bare error discarded that, and 29 of
// 292 run_agent calls on this deployment took that path.
func TestPartialResultReportsCompletedSteps(t *testing.T) {
	got := partialResult(90*time.Second, []string{"click 登录", "type 用户名", "click 提交"})
	for _, want := range []string{"INCOMPLETE", "3", "click 登录", "click 提交"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %s", want, got)
		}
	}
	// The steps already took effect; repeating them could double-submit a form.
	if !strings.Contains(got, "do not repeat") {
		t.Errorf("does not warn against repeating completed steps: %s", got)
	}
}

// Timing out having done nothing is a different situation and deserves
// different advice: there is no state to preserve, so retrying is safe.
func TestPartialResultWithNoSteps(t *testing.T) {
	got := partialResult(90*time.Second, nil)
	if strings.Contains(got, "do not repeat") {
		t.Errorf("warned about steps that never happened: %s", got)
	}
	if !strings.Contains(got, "Nothing was changed") {
		t.Errorf("does not say the state is clean: %s", got)
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

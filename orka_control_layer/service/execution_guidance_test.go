package service

import (
	"strings"
	"testing"
)

func TestExecutionGuidanceReservesDeliveryAndCountsResumeUsage(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	b.carried = 600
	b.AddUsage(100, 50)
	g := executionGuidance(b)
	if !strings.Contains(g, "250") || !strings.Contains(g, "delivery") {
		t.Fatalf("missing remaining allowance/delivery priority: %s", g)
	}
	b.AddUsage(200, 100)
	if strings.Contains(executionGuidance(b), "-50") {
		t.Fatal("negative remaining budget")
	}
}

func TestExecutionGuidanceRequiresVerificationThroughoutRun(t *testing.T) {
	for _, spent := range []int{0, 300, 600, 800} {
		b := newRunBudget(100, 1000, 0)
		b.AddUsage(spent, 0)
		for _, phrase := range []string{"original user requirements", "write and run", "conservation", "group", "boundary", "before charts and reports", "one small", "unchanged source", "not proof"} {
			if !strings.Contains(executionGuidance(b), phrase) {
				t.Errorf("spent %d missing %q: %s", spent, phrase, executionGuidance(b))
			}
		}
	}
	if !strings.Contains(executionGuidance(nil), "write and run") {
		t.Error("verification guidance disappeared without token cap")
	}
}

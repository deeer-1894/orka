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

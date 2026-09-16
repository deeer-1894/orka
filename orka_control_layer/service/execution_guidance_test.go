package service

import (
	"strings"
	"testing"
)

func TestExecutionGuidanceSeparatesResearchFromExecutableDelivery(t *testing.T) {
	for _, b := range []*runBudget{nil, {maxTokens: 100}} {
		text := executionGuidance(b)
		for _, need := range []string{"For browsing, news summaries", "publication date/timezone", "disclose observed paywalls", "A timeout alone does not establish", "stay within that source", "direct clickable source URLs", "Do not create verification scripts", "For requested software or computed data", "write and run a real task-specific verifier", "OR CSV bindings", "not source truth"} {
			if !strings.Contains(text, need) {
				t.Errorf("missing guidance: %s", need)
			}
		}
		if strings.Contains(text, "Task token allowance") {
			t.Fatal("removed quota reintroduced through prompt")
		}
	}
}

package service

import (
	"strings"
	"testing"

	"github.com/orka-oss/orka_core/config"
)

func TestDefaultSubAgentsDoNotBindGovernedTools(t *testing.T) {
	if err := validateSubAgentTools(DefaultSubAgents()); err != nil {
		t.Fatalf("default sub-agents must be valid: %v", err)
	}
}

func TestSubAgentCannotBindGovernedTool(t *testing.T) {
	specs := []config.SubAgentConfig{{
		Name:  "sales_worker",
		Tools: []string{"file_read", "sales_query_answer"},
	}}
	err := validateSubAgentTools(specs)
	if err == nil {
		t.Fatal("expected governed tool binding to be rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sales_query_answer") || !strings.Contains(msg, "sealed-answer contract") || !strings.Contains(msg, "orchestrator") {
		t.Fatalf("error must explain the governed-tool boundary, got %q", msg)
	}
}

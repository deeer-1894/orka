package service

import "github.com/orka-oss/orka_core/config"

// TaskBudgetRequest is retained solely to decode older clients and checkpoints.
// Product execution no longer enforces task token, duration or iteration quotas.
// Zero limits mean unbounded; usage is still recorded independently.
type TaskBudgetRequest struct {
	MaxTokens      int `json:"max_tokens,omitempty"`
	MaxWallSeconds int `json:"max_wall_seconds,omitempty"`
	MaxSteps       int `json:"max_steps,omitempty"`
}

func ResolveTaskBudget(_ config.AgentConfig, _ TaskBudgetRequest) (TaskBudgetRequest, error) {
	return TaskBudgetRequest{}, nil
}

// Historical spend remains in the usage ledger, but historical policy must not
// prevent either journal recovery or confirmation-checkpoint recovery.
func ResolveResumeBudget(_ config.AgentConfig, _ *TaskBudgetRequest, _ int) (TaskBudgetRequest, error) {
	return TaskBudgetRequest{}, nil
}

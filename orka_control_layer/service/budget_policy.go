package service

import (
	"fmt"
	"github.com/orka-oss/orka_core/config"
)

// TaskBudgetRequest is safe to bind from JSON. Zero inherits the deployment
// default, negative values and increases beyond deployment ceilings are errors.
// Add Budget TaskBudgetRequest `json:"budget,omitempty"` to ChatRunRequest.
type TaskBudgetRequest struct {
	MaxTokens      int `json:"max_tokens,omitempty"`
	MaxWallSeconds int `json:"max_wall_seconds,omitempty"`
	MaxSteps       int `json:"max_steps,omitempty"`
}

func ResolveTaskBudget(a config.AgentConfig, req TaskBudgetRequest) (TaskBudgetRequest, error) {
	if err := a.ValidateBudget(); err != nil {
		return TaskBudgetRequest{}, err
	}
	a = a.WithBudgetDefaults()
	p := req
	for _, d := range []struct {
		name              string
		value             *int
		fallback, ceiling int
	}{
		{"max_tokens", &p.MaxTokens, a.RunMaxTokens, a.RunTokenCeiling},
		{"max_wall_seconds", &p.MaxWallSeconds, a.RunMaxWallSeconds, a.RunWallSecondsCeiling},
		{"max_steps", &p.MaxSteps, a.RunMaxSteps, a.RunStepsCeiling},
	} {
		if *d.value == 0 {
			*d.value = d.fallback
		}
		if *d.value < 0 || *d.value > d.ceiling {
			return TaskBudgetRequest{}, fmt.Errorf("budget.%s must be positive and at most %d", d.name, d.ceiling)
		}
	}
	return p, nil
}

// ResolveResumeBudget restores the saved resolved policy, never grants a fresh
// token allowance. A legacy checkpoint retains historical defaults even if the
// deployment default was raised later. A lower deployment ceiling still wins.
// Pass the existing checkpoint's cumulative SpentTokens, not the last run bill.
func ResolveResumeBudget(a config.AgentConfig, saved *TaskBudgetRequest, spentTokens int) (TaskBudgetRequest, error) {
	if err := a.ValidateBudget(); err != nil {
		return TaskBudgetRequest{}, err
	}
	if spentTokens < 0 {
		return TaskBudgetRequest{}, fmt.Errorf("invalid negative checkpoint usage")
	}
	policy := TaskBudgetRequest{MaxTokens: config.DefaultRunMaxTokens, MaxWallSeconds: config.DefaultRunMaxWallSeconds, MaxSteps: config.DefaultRunMaxSteps}
	if saved != nil {
		if saved.MaxTokens <= 0 || saved.MaxWallSeconds <= 0 || saved.MaxSteps <= 0 {
			return TaskBudgetRequest{}, fmt.Errorf("saved budget policy must contain all resolved limits")
		}
		policy = *saved
	}
	a = a.WithBudgetDefaults()
	policy.MaxTokens = min(policy.MaxTokens, a.RunTokenCeiling)
	policy.MaxWallSeconds = min(policy.MaxWallSeconds, a.RunWallSecondsCeiling)
	policy.MaxSteps = min(policy.MaxSteps, a.RunStepsCeiling)
	if spentTokens >= policy.MaxTokens {
		return policy, fmt.Errorf("%w: cumulative checkpoint tokens", ErrRunBudgetExceeded)
	}
	return policy, nil
}

package service

import (
	"context"
	"fmt"
	"github.com/orka-oss/orka_core/config"
	"testing"
	"time"
)

func TestUnlimitedExecutionIgnoresLegacyLimitsAndKeepsUsage(t *testing.T) {
	ctx := context.Background()
	a := config.AgentConfig{RunMaxTokens: 1, RunMaxSteps: 1, RunMaxWallSeconds: 1, UserDailyTokens: 1}
	s, err := NewBudgetSession(ctx, a, TaskBudgetRequest{MaxTokens: 1, MaxSteps: 1, MaxWallSeconds: 1}, &fakeLedger{}, "u", "unlimited")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := s.RunContext(ctx)
	defer cancel()
	if _, ok := runCtx.Deadline(); ok {
		t.Fatal("retired task deadline still installed")
	}
	for i := 0; i < 350; i++ {
		if err := s.AdvanceStep(runCtx, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReserveUsage(runCtx, "call", "main", 60_000_000); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(runCtx, "call", UsageSettlement{Status: UsageKnown, PromptTokens: 60_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveUsage(runCtx, "next", "main", 60_000_000); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().UsedTokens != 60_000_000 || s.Budget().exhausted() != "" {
		t.Fatalf("usage/limit %+v", s.Snapshot())
	}
	cancel()
	if err := s.AdvanceStep(runCtx, "cancelled"); err == nil {
		t.Fatal("manual cancellation ignored")
	}
}

func TestUnlimitedResumePreservesProgressWithoutLegacyDeadline(t *testing.T) {
	saved := &runCheckpoint{SpentTokens: 100_000_000, SuccessfulTools: 3, BudgetPolicy: &TaskBudgetRequest{MaxTokens: 1, MaxSteps: 1, MaxWallSeconds: 1}, BudgetSnapshot: &BudgetSnapshot{UsedSteps: 999, Deadline: time.Now().Add(-time.Hour)}}
	req := &ChatRunRequest{}
	if err := applyResumeBudget(config.AgentConfig{}, req, saved); err != nil {
		t.Fatal(err)
	}
	s, err := NewBudgetSession(context.Background(), config.AgentConfig{}, req.Budget, &fakeLedger{}, "u", "resume")
	if err != nil {
		t.Fatal(err)
	}
	restoreCheckpoint(saved, s.Budget(), nil, nil)
	ctx, cancel := s.RunContext(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("old deadline restored")
	}
	if err := s.ReserveUsage(ctx, "next", "main", 1000); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().CarriedTokens != saved.SpentTokens || s.Budget().successfulTools != 3 {
		t.Fatal("lost execution history")
	}
}

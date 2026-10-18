package service

import (
	"context"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/llm"
)

func TestBudgetAssociatedAuxiliaryPreservesFinishedRun(t *testing.T) {
	ledger := &fakeLedger{}
	root := budgetSession(t, ledger, "owner", "original", 10000, 100000)
	ctx := context.Background()
	if err := root.ReserveUsage(ctx, "main", "main", 100); err != nil {
		t.Fatal(err)
	}
	if err := root.SettleUsage(ctx, "main", UsageSettlement{Status: UsageKnown, PromptTokens: 20}); err != nil {
		t.Fatal(err)
	}
	root.budget.mu.Lock()
	root.budget.deadline = time.Now().Add(-time.Hour)
	root.budget.mu.Unlock()
	if err := root.PersistRun(ctx, "original", "done"); err != nil {
		t.Fatal(err)
	}
	before := root.Snapshot()
	svc, _ := testService(t, llm.NewMock())
	svc.UsageLedger = ledger
	auxCtx, cancel, err := svc.AuxiliaryBudgetContextForRun(ctx, "owner", "followups", "conversation", "original")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	aux := BudgetSessionFrom(auxCtx)
	if aux == root || aux.runID == root.runID {
		t.Fatal("reopened original run")
	}
	initial, err := svc.RunBudgetSnapshot(ctx, "owner", aux.runID)
	if err != nil || initial.RelatedRunID != "original" || initial.RelatedConversationID != "conversation" {
		t.Fatalf("missing durable association: %+v %v", initial, err)
	}
	provider := llm.NewAccounted(llm.NewMock(llm.Response{Usage: llm.Usage{Known: true, TotalTokens: 7}}))
	if _, err := provider.Chat(auxCtx, llm.Request{MaxTokens: 100}); err != nil {
		t.Fatal(err)
	}
	cancel()
	final, err := svc.RunBudgetSnapshot(ctx, "owner", aux.runID)
	if err != nil || final.Status != "done" || final.UsedTokens != 7 || final.RelatedRunID != "original" {
		t.Fatalf("aux final: %+v %v", final, err)
	}
	original, err := svc.RunBudgetSnapshot(ctx, "owner", "original")
	if err != nil || original.Status != "done" || original.UsedTokens != 20 || !original.Deadline.Equal(before.Deadline) {
		t.Fatalf("original mutated: %+v %v", original, err)
	}
	daily, err := aux.DailySnapshot(ctx)
	if err != nil || daily.UsedTokens != 27 {
		t.Fatalf("daily attribution: %+v %v", daily, err)
	}
	account, _ := ledger.Load(ctx, "owner")
	found := false
	for _, entry := range account.Entries {
		if entry.RunID == aux.runID {
			found = true
			if entry.RelatedRunID != "original" || entry.RelatedConversationID != "conversation" || entry.Source != "followups" {
				t.Fatalf("entry association lost: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("auxiliary usage not ledgered")
	}
	if _, cancel, err := svc.AuxiliaryBudgetContextForRun(root.Context(ctx), "owner", "followups", "conversation", "original"); err == nil {
		cancel()
		t.Fatal("aux inherited a parent budget")
	}
}

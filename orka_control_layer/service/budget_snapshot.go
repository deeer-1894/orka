package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
)

type BudgetSourceSnapshot struct {
	Source          string `json:"source"`
	UsedTokens      int    `json:"used_tokens"`
	ReservedTokens  int    `json:"reserved_tokens"`
	UnknownCalls    int    `json:"unknown_calls"`
	UnknownTokens   int    `json:"unknown_tokens"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

func projectBudget(snapshot BudgetSnapshot, entries []db.UsageEntry) BudgetSnapshot {
	snapshot.UsedTokens = snapshot.CarriedTokens
	snapshot.ReservedTokens = 0
	snapshot.UnknownCalls = snapshot.CarriedUnknownCalls
	snapshot.UnknownTokens = snapshot.CarriedUnknownTokens
	snapshot.EstimatedTokens = 0
	bySource := map[string]*BudgetSourceSnapshot{}
	if snapshot.CarriedTokens > 0 {
		bySource["previous_attempts"] = &BudgetSourceSnapshot{Source: "previous_attempts", UsedTokens: snapshot.CarriedTokens, UnknownCalls: snapshot.CarriedUnknownCalls, UnknownTokens: snapshot.CarriedUnknownTokens}
	}
	for _, e := range entries {
		if e.RunID != snapshot.BudgetRunID || e.Status == db.UsageCancelled {
			continue
		}
		source := bySource[e.Source]
		if source == nil {
			source = &BudgetSourceSnapshot{Source: e.Source}
			bySource[e.Source] = source
		}
		if e.Status == db.UsageReserved {
			snapshot.ReservedTokens = saturatingUsageSum(snapshot.ReservedTokens, e.Tokens)
			source.ReservedTokens = saturatingUsageSum(source.ReservedTokens, e.Tokens)
		} else {
			snapshot.UsedTokens = saturatingUsageSum(snapshot.UsedTokens, e.Tokens)
			source.UsedTokens = saturatingUsageSum(source.UsedTokens, e.Tokens)
		}
		if e.Status == UsageUnknown {
			snapshot.UnknownCalls++
			snapshot.UnknownTokens = saturatingUsageSum(snapshot.UnknownTokens, e.Tokens)
			source.UnknownCalls++
			source.UnknownTokens = saturatingUsageSum(source.UnknownTokens, e.Tokens)
		}
		if e.Status == UsageEstimated {
			snapshot.EstimatedTokens = saturatingUsageSum(snapshot.EstimatedTokens, e.Tokens)
			source.EstimatedTokens = saturatingUsageSum(source.EstimatedTokens, e.Tokens)
		}
	}
	snapshot.Sources = make([]BudgetSourceSnapshot, 0, len(bySource))
	for _, source := range bySource {
		snapshot.Sources = append(snapshot.Sources, *source)
	}
	sort.Slice(snapshot.Sources, func(i, j int) bool { return snapshot.Sources[i].Source < snapshot.Sources[j].Source })
	snapshot.RemainingTokens = max(0, snapshot.Limits.MaxTokens-saturatingUsageSum(snapshot.UsedTokens, snapshot.ReservedTokens))
	return snapshot
}

func (s *BudgetSession) persistSnapshot(ctx context.Context, runID, status string, initial bool) error {
	store, ok := s.ledger.(db.UsageSnapshotStore)
	if !ok {
		return errors.New("usage ledger does not support durable run snapshots")
	}
	snapshot := s.Snapshot()
	snapshot.RunID = runID
	snapshot.Status = status
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return store.PutUsageSnapshot(ctx, s.owner, runID, raw, initial)
}

// PersistRun stores the final view (or updates restored carried usage before
// dispatch). runID may identify a workflow child sharing this parent budget.
func (s *BudgetSession) PersistRun(ctx context.Context, runID, status string) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.persistSnapshot(persistCtx, runID, status, false)
}

// ReadRunBudget is owner-scoped by its storage key. Active usage comes from the
// same reservation ledger; completed snapshots survive rolling-window pruning.
func ReadRunBudget(ctx context.Context, ledger db.UsageLedger, owner, runID string) (BudgetSnapshot, error) {
	var snapshot BudgetSnapshot
	store, ok := ledger.(db.UsageSnapshotStore)
	if !ok {
		return snapshot, errors.New("run budget storage unavailable")
	}
	raw, err := store.UsageSnapshot(ctx, owner, runID)
	if err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.RunID != runID || snapshot.BudgetRunID == "" {
		return snapshot, errors.New("invalid run budget identity")
	}
	if snapshot.BudgetRunID != runID {
		shared, err := ReadRunBudget(ctx, ledger, owner, snapshot.BudgetRunID)
		if err != nil {
			return snapshot, err
		}
		shared.RunID = runID
		shared.Status = snapshot.Status
		return shared, nil
	}
	if snapshot.Status != "running" {
		return snapshot, nil
	}
	account, err := ledger.Load(ctx, owner)
	if err != nil {
		return snapshot, err
	}
	if _, err := dailyUsage(account.Entries, time.Now()); err != nil {
		return snapshot, err
	}
	return projectBudget(snapshot, account.Entries), nil
}
func (s *ChatService) RunBudgetSnapshot(ctx context.Context, owner, runID string) (BudgetSnapshot, error) {
	return ReadRunBudget(ctx, s.budgetLedger(), owner, runID)
}

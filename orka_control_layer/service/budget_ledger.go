package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
)

func usageSum(a, b int) (int, error) {
	if a < 0 || b < 0 || a > int(^uint(0)>>1)-b {
		return 0, errors.New("invalid or overflowing usage tokens")
	}
	return a + b, nil
}
func saturatingUsageSum(a, b int) int {
	n, err := usageSum(a, b)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return n
}
func usageIndex(entries []db.UsageEntry, runID, callID string) int {
	for i, e := range entries {
		if e.RunID == runID && e.CallID == callID {
			return i
		}
	}
	return -1
}

func activeUsage(e db.UsageEntry, now time.Time) bool {
	// Neither a very long call nor an unresolved missing-usage response becomes
	// free at midnight or 24h after dispatch. Only settled measurements age out.
	return e.Status == db.UsageReserved || e.Status == UsageUnknown || e.SettledAt >= now.Add(-24*time.Hour).UnixMilli()
}
func dailyUsage(entries []db.UsageEntry, now time.Time) (int, error) {
	total := 0
	for _, e := range entries {
		switch e.Status {
		case db.UsageReserved, UsageKnown, UsageEstimated, UsageUnknown, db.UsageCancelled:
		default:
			return 0, fmt.Errorf("unrecognized ledger usage state %q", e.Status)
		}
		if e.Tokens < 0 {
			return 0, errors.New("negative ledger usage")
		}
		if activeUsage(e, now) {
			var err error
			total, err = usageSum(total, e.Tokens)
			if err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

// updateLedger retries optimistic conflicts, never I/O failures whose commit
// outcome may be ambiguous. Repeating the original call ID lets a caller safely
// reconcile those errors. No process-local mutex serves as the daily quota lock.
func (s *BudgetSession) updateLedger(ctx context.Context, change func(*db.UsageAccount, time.Time) (db.UsageEntry, error)) (db.UsageEntry, error) {
	for attempt := 0; attempt < 256; attempt++ {
		if err := ctx.Err(); err != nil {
			return db.UsageEntry{}, err
		}
		a, err := s.ledger.Load(ctx, s.owner)
		if err != nil {
			return db.UsageEntry{}, fmt.Errorf("load usage ledger: %w", err)
		}
		now := time.Now()
		if _, err := dailyUsage(a.Entries, now); err != nil {
			return db.UsageEntry{}, err
		}
		// Keep this run's idempotency records plus all unresolved and recent calls.
		kept := make([]db.UsageEntry, 0, len(a.Entries))
		for _, e := range a.Entries {
			if e.RunID == s.runID || activeUsage(e, now) {
				kept = append(kept, e)
			}
		}
		a.Entries = kept
		entry, err := change(&a, now)
		if err != nil {
			return db.UsageEntry{}, err
		}
		ok, err := s.ledger.CompareAndSwap(ctx, s.owner, a.Version, a)
		if err != nil {
			return db.UsageEntry{}, fmt.Errorf("commit usage ledger: %w", err)
		}
		if ok {
			s.entries = make(map[string]db.UsageEntry)
			for _, e := range a.Entries {
				if e.RunID == s.runID {
					s.entries[e.CallID] = e
				}
			}
			return entry, nil
		}
		runtime.Gosched()
	}
	return db.UsageEntry{}, errors.New("usage ledger contention: retry admission with the same call ID")
}

// DailyBudgetSnapshot separates billable usage from in-flight commitments. An
// unknown settled charge is part of UsedTokens, with its uncertainty visible.
type DailyBudgetSnapshot struct {
	LimitTokens     int `json:"limit_tokens"`
	UsedTokens      int `json:"used_tokens"`
	ReservedTokens  int `json:"reserved_tokens"`
	RemainingTokens int `json:"remaining_tokens"`
	UnknownCalls    int `json:"unknown_calls"`
	UnknownTokens   int `json:"unknown_tokens"`
	EstimatedTokens int `json:"estimated_tokens"`
}

func ReadDailyBudget(ctx context.Context, ledger db.UsageLedger, owner string, limit int) (DailyBudgetSnapshot, error) {
	out := DailyBudgetSnapshot{LimitTokens: limit}
	if ledger == nil || owner == "" || limit < 0 {
		return out, errors.New("usage requires ledger, owner and nonnegative legacy limit")
	}
	a, err := ledger.Load(ctx, owner)
	if err != nil {
		return out, err
	}
	now := time.Now()
	total, err := dailyUsage(a.Entries, now)
	if err != nil {
		return out, err
	}
	for _, e := range a.Entries {
		if !activeUsage(e, now) {
			continue
		}
		if e.Status == db.UsageReserved {
			out.ReservedTokens += e.Tokens
		} else {
			out.UsedTokens += e.Tokens
		}
		if e.Status == UsageUnknown {
			out.UnknownCalls++
			out.UnknownTokens += e.Tokens
		}
		if e.Status == UsageEstimated {
			out.EstimatedTokens += e.Tokens
		}
	}
	out.RemainingTokens = max(0, limit-total)
	return out, nil
}

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/config"
)

// fakeLedger stores detached snapshots like a database, with an atomic version
// predicate. Separate sessions deliberately do not share a service mutex.
type fakeLedger struct {
	snapshotFail bool
	mu           sync.Mutex
	accounts     map[string]db.UsageAccount
	snapshots    map[string][]byte
	fail         bool
}

func (f *fakeLedger) Load(ctx context.Context, owner string) (db.UsageAccount, error) {
	if err := ctx.Err(); err != nil {
		return db.UsageAccount{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return db.UsageAccount{}, errors.New("ledger unavailable")
	}
	a := f.accounts[owner]
	a.Entries = append([]db.UsageEntry(nil), a.Entries...)
	return a, nil
}
func (f *fakeLedger) CompareAndSwap(ctx context.Context, owner string, version int64, a db.UsageAccount) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return false, errors.New("ledger unavailable")
	}
	if f.accounts == nil {
		f.accounts = map[string]db.UsageAccount{}
	}
	if f.accounts[owner].Version != version {
		return false, nil
	}
	a.Version = version + 1
	a.Entries = append([]db.UsageEntry(nil), a.Entries...)
	f.accounts[owner] = a
	return true, nil
}
func budgetSession(t *testing.T, ledger db.UsageLedger, owner, run string, tokens, daily int) *BudgetSession {
	t.Helper()
	s, err := NewBudgetSession(context.Background(), config.AgentConfig{RunMaxTokens: tokens, UserDailyTokens: daily}, TaskBudgetRequest{}, ledger, owner, run)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestLegacyBudgetPolicyIsIgnored(t *testing.T) {
	for _, req := range []TaskBudgetRequest{{}, {MaxTokens: 1}, {MaxTokens: -1}, {MaxSteps: 1, MaxWallSeconds: 1}} {
		p, err := ResolveTaskBudget(config.AgentConfig{RunMaxTokens: 1, UserDailyTokens: 1}, req)
		if err != nil || p != (TaskBudgetRequest{}) {
			t.Fatalf("legacy limit %+v %v", p, err)
		}
	}
}

func TestBudgetDailyReservationsAcrossConcurrentRuns(t *testing.T) {
	f := &fakeLedger{}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := budgetSession(t, f, "fake@example.test", fmt.Sprint(i), 100, 100)
			<-start
			err := s.ReserveUsage(context.Background(), "call", "main", 10)
			if err == nil {
				accepted.Add(1)
			} else if err != nil {
				t.Errorf("reserve: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 64 {
		t.Fatalf("accepted %d calls, want 64", accepted.Load())
	}
}
func TestBudgetSharedParentAndAllUsageSources(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "fake", "root", 100, 1000)
	ctx := s.Context(context.Background())
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if BudgetSessionFrom(child) != s || budgetFrom(child) != s.Budget() {
		t.Fatal("child lost parent allowance")
	}
	for i, source := range []string{"main", "summary", "retry", "subagent", "gui", "followup"} {
		id := fmt.Sprint(i)
		if err := BudgetSessionFrom(child).ReserveUsage(child, id, source, 10); err != nil {
			t.Fatal(err)
		}
		if err := s.SettleUsage(child, id, UsageSettlement{Status: UsageKnown, PromptTokens: 6, CompletionTokens: 4}); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Snapshot()
	if got.UsedTokens != 60 || got.ReservedTokens != 0 || got.RemainingTokens != 0 || s.Budget().spentTokens() != 60 {
		t.Fatalf("snapshot %+v", got)
	}
	a, _ := f.Load(ctx, "fake")
	if len(a.Entries) != 6 {
		t.Fatalf("sources lost: %+v", a)
	}
	if err := s.ReserveUsage(ctx, "over", "subagent", 41); err != nil {
		t.Fatalf("child overspent parent: %v", err)
	}
}
func TestBudgetUnknownSettlementAndReconciliation(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "fake", "root", 100, 100)
	ctx := context.Background()
	if err := s.ReserveUsage(ctx, "call", "gui", 80); err != nil {
		t.Fatal(err)
	}
	unknown := UsageSettlement{Status: UsageUnknown}
	if err := s.SettleUsage(ctx, "call", unknown); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, "call", unknown); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot()
	if got.UsedTokens != 80 || got.UnknownCalls != 1 || got.UnknownTokens != 80 || got.RemainingTokens != 0 {
		t.Fatalf("unknown became free: %+v", got)
	}
	other := budgetSession(t, f, "fake", "other", 100, 100)
	if err := other.ReserveUsage(ctx, "call", "main", 21); err != nil {
		t.Fatalf("unknown daily charge lost: %v", err)
	}
	known := UsageSettlement{Status: UsageKnown, PromptTokens: 12, CompletionTokens: 8}
	if err := s.SettleUsage(ctx, "call", known); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, "call", known); err != nil {
		t.Fatal(err)
	}
	got = s.Snapshot()
	if got.UsedTokens != 20 || got.UnknownCalls != 0 || got.RemainingTokens != 0 {
		t.Fatalf("reconciliation %+v", got)
	}
	if err := s.SettleUsage(ctx, "call", UsageSettlement{Status: UsageKnown, PromptTokens: 1}); !errors.Is(err, ErrUsageConflict) {
		t.Fatalf("conflicting settlement %v", err)
	}
}
func TestBudgetSettlementFailureRetainsReservationAndSurvivesCancellation(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "fake", "root", 100, 100)
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.ReserveUsage(ctx, "call", "main", 80); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	if err := s.SettleUsage(ctx, "call", UsageSettlement{Status: UsageKnown, PromptTokens: 50}); err == nil {
		t.Fatal("lost settlement error")
	}
	if got := s.Snapshot(); got.ReservedTokens != 80 {
		t.Fatalf("released failed settlement: %+v", got)
	}
	if err := s.ReserveUsage(ctx, "other", "main", 1); err == nil {
		t.Fatal("ledger failure allowed work")
	}
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	cancel()
	if err := s.SettleUsage(ctx, "call", UsageSettlement{Status: UsageKnown, PromptTokens: 50}); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UsedTokens != 50 || got.ReservedTokens != 0 {
		t.Fatal(got)
	}
}
func TestBudgetKnownZeroUnknownDefaultAndCancellation(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "fake", "root", 100000, 200000)
	ctx := context.Background()
	if err := s.ReserveUsage(ctx, "zero", "main", 30); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, "zero", UsageSettlement{}); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UnknownCalls != 1 || got.UsedTokens != 30 {
		t.Fatal(got)
	}
	if err := s.SettleUsage(ctx, "zero", UsageSettlement{Status: UsageKnown}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveUsage(ctx, "cancel", "main", 0); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.ReservedTokens != 32768 {
		t.Fatal(got)
	}
	if err := s.CancelUsage(ctx, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelUsage(ctx, "cancel"); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UsedTokens != 0 || got.ReservedTokens != 0 || got.UnknownCalls != 0 {
		t.Fatal(got)
	}
	if err := s.SettleUsage(ctx, "absent", UsageSettlement{Status: UsageKnown, PromptTokens: 5}); !errors.Is(err, ErrUsageNotReserved) {
		t.Fatalf("unreserved usage swallowed: %v", err)
	}
}
func TestBudgetRecordsProviderOverrunWithoutClipping(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 100, 100)
	ctx := context.Background()
	if err := s.ReserveUsage(ctx, "call", "main", 90); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, "call", UsageSettlement{Status: UsageKnown, PromptTokens: 150}); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UsedTokens != 150 || got.RemainingTokens != 0 {
		t.Fatal(got)
	}
	if err := s.ReserveUsage(ctx, "next", "main", 1); err != nil {
		t.Fatalf("overrun allowed next call %v", err)
	}
}
func TestBudgetSharedConcurrentReservationsAndIdempotentSettlement(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 100, 1000)
	ctx := context.Background()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.ReserveUsage(ctx, fmt.Sprint(i), "subagent", 10)
			if err == nil {
				accepted.Add(1)
			} else if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 40 {
		t.Fatal(accepted.Load())
	}
	s2 := budgetSession(t, &fakeLedger{}, "fake", "other", 100, 100)
	if err := s2.ReserveUsage(ctx, "call", "main", 80); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s2.SettleUsage(ctx, "call", UsageSettlement{Status: UsageKnown, PromptTokens: 50}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := s2.Snapshot(); got.UsedTokens != 50 || got.ReservedTokens != 0 {
		t.Fatal(got)
	}
}
func TestBudgetDailyWindowDoesNotExpireInflight(t *testing.T) {
	now := time.Now()
	f := &fakeLedger{accounts: map[string]db.UsageAccount{"fake": {Version: 1, Entries: []db.UsageEntry{
		{RunID: "old", CallID: "known", Source: "main", Status: UsageKnown, Tokens: 70, SettledAt: now.Add(-25 * time.Hour).UnixMilli()},
		{RunID: "old", CallID: "inflight", Source: "gui", Status: db.UsageReserved, Tokens: 40, ReservedAt: now.Add(-25 * time.Hour).UnixMilli()},
	}}}}
	s := budgetSession(t, f, "fake", "root", 100, 100)
	if err := s.ReserveUsage(context.Background(), "new", "main", 60); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveUsage(context.Background(), "over", "main", 1); err != nil {
		t.Fatal(err)
	}
}
func TestBudgetStepsSurviveWindowCompactionAndRetries(t *testing.T) {
	s, err := NewBudgetSession(context.Background(), config.AgentConfig{}, TaskBudgetRequest{MaxSteps: 2}, &fakeLedger{}, "fake", "root")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"1", "1", "2", "2"} {
		if err := s.AdvanceStep(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	s.Budget().observe(nil)
	if err := s.AdvanceStep(ctx, "3"); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UsedSteps != 3 {
		t.Fatal(got)
	}
}
func TestUsageInputValidationWithoutTaskDeadline(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 100, 100)
	ctx := context.Background()
	for _, u := range []UsageSettlement{{Status: UsageKnown, PromptTokens: -1}, {Status: "bogus"}, {Status: UsageKnown, PromptTokens: int(^uint(0) >> 1), CompletionTokens: 1}} {
		if err := s.ReserveUsage(ctx, "call", "main", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.SettleUsage(ctx, "call", u); err == nil {
			t.Fatalf("invalid settlement %+v", u)
		}
	}
	s.Budget().deadline = time.Now().Add(-time.Second)
	if err := s.ReserveUsage(ctx, "late", "main", 1); err != nil {
		t.Fatal(err)
	}
}

func TestUsageSameRunSessionsCanContinuePastLegacyLimits(t *testing.T) {
	f := &fakeLedger{}
	a := budgetSession(t, f, "fake", "same", 100, 1000)
	b := budgetSession(t, f, "fake", "same", 100, 1000)
	if err := a.ReserveUsage(context.Background(), "first", "main", 80); err != nil {
		t.Fatal(err)
	}
	if err := b.ReserveUsage(context.Background(), "second", "main", 30); err != nil {
		t.Fatalf("separate handles bypass parent: %v", err)
	}
}

func TestBudgetDailySnapshotReportsUnknownAndInflight(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "fake", "root", 100, 200)
	ctx := context.Background()
	if err := s.ReserveUsage(ctx, "first", "main", 80); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveUsage(ctx, "second", "gui", 20); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleUsage(ctx, "second", UsageSettlement{Status: UsageUnknown}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DailySnapshot(ctx)
	if err != nil || got.UsedTokens != 20 || got.ReservedTokens != 80 || got.RemainingTokens != 0 || got.UnknownCalls != 1 {
		t.Fatalf("daily %+v %v", got, err)
	}
}

func TestBudgetFreshRunAfterUnknownAgeStillCharged(t *testing.T) {
	f := &fakeLedger{accounts: map[string]db.UsageAccount{"fake": {Version: 1, Entries: []db.UsageEntry{{RunID: "old", CallID: "unknown", Status: UsageUnknown, Tokens: 100, SettledAt: time.Now().Add(-48 * time.Hour).UnixMilli()}}}}}
	s := budgetSession(t, f, "fake", "new", 100, 100)
	if err := s.ReserveUsage(context.Background(), "first", "main", 1); err != nil {
		t.Fatal(err)
	}
}

func TestUsageContextPreservesParentCancellation(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 100, 100)
	parent, stop := context.WithCancel(context.Background())
	ctx, cancel := s.RunContext(parent)
	defer cancel()
	stop()
	if !errors.Is(ctx.Err(), context.Canceled) || BudgetSessionFrom(ctx) != s {
		t.Fatalf("cancellation/context %v", ctx.Err())
	}
}

func TestBudgetResumePolicyRetainsSavedAllowanceAndCarry(t *testing.T) {
	saved := TaskBudgetRequest{MaxTokens: 500, MaxSteps: 30, MaxWallSeconds: 120}
	policy, err := ResolveResumeBudget(config.AgentConfig{}, &saved, 100_000_000)
	if err != nil || policy != (TaskBudgetRequest{}) {
		t.Fatalf("old limit restored %+v %v", policy, err)
	}
	s, err := NewBudgetSession(context.Background(), config.AgentConfig{}, policy, &fakeLedger{}, "fake", "resumed")
	if err != nil {
		t.Fatal(err)
	}
	restoreCheckpoint(&runCheckpoint{SpentTokens: 400}, s.Budget(), nil, nil)
	if err := s.ReserveUsage(context.Background(), "first", "main", 100); err != nil {
		t.Fatal(err)
	}
	if got := s.Budget().totalSpentTokens(); got != 500 {
		t.Fatalf("checkpoint must preserve inflight commitments too: %d", got)
	}
	if err := s.SettleUsage(context.Background(), "first", UsageSettlement{Status: UsageKnown, PromptTokens: 90}); err != nil {
		t.Fatal(err)
	}
	if s.Budget().spentTokens() != 90 || s.Budget().totalSpentTokens() != 490 {
		t.Fatalf("resume rebilled prior usage: %+v", s.Snapshot())
	}
}

func TestBudgetAttemptSinkCanReceiveExternalUsage(t *testing.T) {
	s := budgetSession(t, &fakeLedger{}, "fake", "root", 100, 100)
	ctx := context.Background()
	attempt, err := s.BeginUsage(ctx, "gui-attempt", "gui", 80)
	if err != nil {
		t.Fatal(err)
	}
	// Same two-int sink contract used by llm.ReportExternalUsage.
	attempt.AddUsage(10, 20)
	attempt.AddUsage(5, 5)
	if err := attempt.Finish(ctx, UsageKnown); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Finish(ctx, UsageKnown); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UsedTokens != 40 || got.ReservedTokens != 0 {
		t.Fatal(got)
	}
	unknown, err := s.BeginUsage(ctx, "missing", "main", 30)
	if err != nil {
		t.Fatal(err)
	}
	unknown.AddUsage(0, 0)
	if err := unknown.Finish(ctx, UsageUnknown); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); got.UnknownTokens != 30 || got.UsedTokens != 70 {
		t.Fatal(got)
	}
}

func (f *fakeLedger) PutUsageSnapshot(ctx context.Context, owner, runID string, raw []byte, initial bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapshots == nil {
		f.snapshots = map[string][]byte{}
	}
	if f.snapshotFail {
		return errors.New("snapshot unavailable")
	}
	key := owner + "\x00" + runID
	if initial && f.snapshots[key] != nil {
		return nil
	}
	f.snapshots[key] = append([]byte(nil), raw...)
	return nil
}
func (f *fakeLedger) UsageSnapshot(ctx context.Context, owner, runID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.snapshots[owner+"\x00"+runID]
	if !ok {
		return nil, db.ErrNotFound
	}
	return append([]byte(nil), raw...), nil
}

func TestBudgetSnapshotActiveFinalAndSharedOwnerScope(t *testing.T) {
	f := &fakeLedger{}
	s := budgetSession(t, f, "alice", "root", 100, 1000)
	ctx := context.Background()
	if err := s.ReserveUsage(ctx, "call", "gui", 80); err != nil {
		t.Fatal(err)
	}
	active, err := ReadRunBudget(ctx, f, "alice", "root")
	if err != nil || active.ReservedTokens != 80 || active.RemainingTokens != 0 || active.Limits.MaxTokens != 0 || len(active.Sources) != 1 {
		t.Fatalf("active %+v %v", active, err)
	}
	if _, err := ReadRunBudget(ctx, f, "bob", "root"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("owner escaped: %v", err)
	}
	if err := s.PersistRun(ctx, "child", "running"); err != nil {
		t.Fatal(err)
	}
	child, err := ReadRunBudget(ctx, f, "alice", "child")
	if err != nil || child.RunID != "child" || child.BudgetRunID != "root" || child.ReservedTokens != 80 {
		t.Fatalf("child %+v %v", child, err)
	}
	if err := s.SettleUsage(ctx, "call", UsageSettlement{Status: UsageUnknown}); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistRun(ctx, "root", "partial"); err != nil {
		t.Fatal(err)
	}
	// Even after rolling-window storage compaction, the final visible bill survives.
	f.mu.Lock()
	f.accounts = map[string]db.UsageAccount{}
	f.mu.Unlock()
	final, err := ReadRunBudget(ctx, f, "alice", "root")
	if err != nil || final.Status != "partial" || final.UsedTokens != 80 || final.UnknownTokens != 80 || final.ReservedTokens != 0 || final.Sources[0].Source != "gui" {
		t.Fatalf("final %+v %v", final, err)
	}
}

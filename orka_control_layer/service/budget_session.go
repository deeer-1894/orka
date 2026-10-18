package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/config"
)

var (
	ErrRunBudgetExceeded  = errors.New("run budget exceeded")
	ErrDailyQuotaExceeded = errors.New("daily token quota exceeded")
	ErrUsageNotReserved   = errors.New("usage has no reservation")
	ErrUsageConflict      = errors.New("usage attempt conflicts with ledger")
)

const (
	UsageKnown     = db.UsageKnown
	UsageEstimated = db.UsageEstimated
	UsageUnknown   = db.UsageUnknown
)

// UsageSettlement requires an explicit known/estimated status even for a free
// call. Empty status means unknown, retaining at least the original reservation.
type UsageSettlement struct {
	Status           string `json:"status"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
}

type BudgetSnapshot struct {
	RelatedConversationID string                 `json:"related_conversation_id,omitempty"`
	RelatedRunID          string                 `json:"related_run_id,omitempty"`
	RunID                 string                 `json:"run_id"`
	BudgetRunID           string                 `json:"budget_run_id"`
	Status                string                 `json:"status"`
	CarriedTokens         int                    `json:"carried_tokens"`
	CarriedUnknownCalls   int                    `json:"carried_unknown_calls"`
	CarriedUnknownTokens  int                    `json:"carried_unknown_tokens"`
	Sources               []BudgetSourceSnapshot `json:"sources"`

	Limits          TaskBudgetRequest `json:"limits"`
	UsedTokens      int               `json:"used_tokens"`
	ReservedTokens  int               `json:"reserved_tokens"`
	RemainingTokens int               `json:"remaining_tokens"`
	UsedSteps       int               `json:"used_steps"`
	UnknownCalls    int               `json:"unknown_calls"`
	UnknownTokens   int               `json:"unknown_tokens"`
	EstimatedTokens int               `json:"estimated_tokens"`
	Deadline        time.Time         `json:"deadline"`
}

// BudgetSession retains its legacy name for checkpoint compatibility. It owns
// usage metering, not execution quotas, and shares its runBudget with existing
// middleware/checkpoint code. All paid attempts, including retries, reserve
// BEFORE dispatch and settle after response/stream termination. Source labels
// are open strings (main, summary, retry, subagent, gui, followup, ...).
//
// The invocation adapter must use this reservation API instead of ALSO calling
// the legacy AddUsage sink for that exchange, which would double-charge it.
type BudgetSession struct {
	association        budgetAssociation
	mu                 sync.Mutex
	budget             *runBudget
	policy             TaskBudgetRequest
	ledger             db.UsageLedger
	owner, runID       string
	defaultReservation int
	entries            map[string]db.UsageEntry
	steps              map[string]struct{}
}

func NewBudgetSession(ctx context.Context, a config.AgentConfig, req TaskBudgetRequest, ledger db.UsageLedger, owner, runID string) (*BudgetSession, error) {
	policy, err := ResolveTaskBudget(a, req)
	if err != nil {
		return nil, err
	}
	if owner == "" || runID == "" || ledger == nil {
		return nil, errors.New("budget requires owner, run ID and usage ledger")
	}
	if parent := BudgetSessionFrom(ctx); parent != nil {
		if parent.owner != owner {
			return nil, errors.New("child budget owner differs from parent")
		}

		return parent, nil
	}
	a = a.WithBudgetDefaults()
	s := &BudgetSession{budget: newRunBudget(policy.MaxSteps, policy.MaxTokens, time.Duration(policy.MaxWallSeconds)*time.Second), policy: policy, ledger: ledger, owner: owner, runID: runID, defaultReservation: a.UsageReservationTokens, entries: map[string]db.UsageEntry{}, steps: map[string]struct{}{}}
	s.association, _ = ctx.Value(budgetAssociationKey{}).(budgetAssociation)
	account, err := ledger.Load(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("load budget: %w", err)
	}
	for _, entry := range account.Entries {
		if entry.RunID == runID {
			s.entries[entry.CallID] = entry
		}
	}
	s.syncMeter()
	s.budget.explicitSteps = true
	if err := s.persistSnapshot(ctx, runID, "running", true); err != nil {
		return nil, err
	}
	return s, nil
}

// Budget returns the usage meter for checkpoint restoration and finalization.
// Carried usage remains visible without charging it to the daily ledger again.
func (s *BudgetSession) Budget() *runBudget { return s.budget }

type budgetSessionKey struct{}

func (s *BudgetSession) Context(ctx context.Context) context.Context {
	return llm.WithCallAccountant(context.WithValue(withBudget(ctx, s.budget), budgetSessionKey{}, s), &budgetCallAccountant{session: s})
}

// RunContext installs metering and preserves caller cancellation. It adds no
// task deadline; individual transports retain their request timeouts.
func (s *BudgetSession) RunContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(s.Context(ctx))
}

func BudgetSessionFrom(ctx context.Context) *BudgetSession {
	s, _ := ctx.Value(budgetSessionKey{}).(*BudgetSession)
	return s
}

func (s *BudgetSession) Snapshot() BudgetSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}
func (s *BudgetSession) snapshot() BudgetSnapshot {
	s.budget.mu.Lock()
	out := BudgetSnapshot{RelatedConversationID: s.association.conversationID, RelatedRunID: s.association.runID, RunID: s.runID, BudgetRunID: s.runID, Status: "running", Limits: s.policy, CarriedTokens: s.budget.carried, CarriedUnknownCalls: s.budget.carriedUnknownCalls, CarriedUnknownTokens: s.budget.carriedUnknownTokens, UsedSteps: s.budget.carriedSteps + len(s.steps), Deadline: s.budget.deadline}
	s.budget.mu.Unlock()
	entries := make([]db.UsageEntry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	return projectBudget(out, entries)
}

// AdvanceStep counts a logical generation cycle once, independent of message
// compaction. Retries of that cycle reuse stepID but get distinct usage call IDs.
// All branches share this counter; auxiliary summaries need not advance a step.
func (s *BudgetSession) AdvanceStep(ctx context.Context, stepID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if stepID == "" {
		return errors.New("budget step ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.steps[stepID]; ok {
		return nil
	}

	s.steps[stepID] = struct{}{}
	s.budget.mu.Lock()
	s.budget.sharedSteps = s.budget.carriedSteps + len(s.steps)
	s.budget.mu.Unlock()
	return nil
}

// ReserveUsage includes the caller's estimated prompt PLUS maximum completion
// tokens. A zero estimate uses the configured fallback and is not a claim that
// the provider cannot overrun it. Repeating admission after an ambiguous storage
// error is safe with the same ID; never dispatch two exchanges under that ID.
func (s *BudgetSession) ReserveUsage(ctx context.Context, callID, source string, tokens int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if callID == "" || source == "" || tokens < 0 {
		return errors.New("usage requires call ID, source and nonnegative reservation")
	}
	if tokens == 0 {
		tokens = s.defaultReservation
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.updateLedger(ctx, func(a *db.UsageAccount, now time.Time) (db.UsageEntry, error) {
		if i := usageIndex(a.Entries, s.runID, callID); i >= 0 {
			old := a.Entries[i]
			if old.Status != db.UsageReserved || old.Source != source || old.ReservedTokens != tokens {
				return old, ErrUsageConflict
			}
			return old, nil
		}

		e := db.UsageEntry{RelatedConversationID: s.association.conversationID, RelatedRunID: s.association.runID, RunID: s.runID, CallID: callID, Source: source, Status: db.UsageReserved, Tokens: tokens, ReservedTokens: tokens, ReservedAt: now.UnixMilli()}
		a.Entries = append(a.Entries, e)
		return e, nil
	})
	if err != nil {
		return err
	}
	s.entries[callID] = entry
	s.syncMeter()
	return nil
}

// SettleUsage is allowed after cancellation/deadline and uses a bounded detached
// context. Storage errors are returned, leaving the durable reservation intact;
// callers must retry settlement with the same ID. Actual overruns are fully
// recorded without limiting subsequent calls.
// Unknown/estimated charges may be reconciled to known usage later.
func (s *BudgetSession) SettleUsage(ctx context.Context, callID string, u UsageSettlement) error {
	if u.Status == "" {
		u.Status = UsageUnknown
	}
	if u.Status != UsageKnown && u.Status != UsageEstimated && u.Status != UsageUnknown {
		return errors.New("invalid usage status")
	}
	if _, err := usageSum(u.PromptTokens, u.CompletionTokens); err != nil {
		return err
	}
	return s.finishUsage(ctx, callID, u, false)
}

// CancelUsage releases only an exchange proven NOT to have been dispatched. A
// timeout/disconnect/cancellation after dispatch must settle as unknown instead.
func (s *BudgetSession) CancelUsage(ctx context.Context, callID string) error {
	return s.finishUsage(ctx, callID, UsageSettlement{}, true)
}
func (s *BudgetSession) finishUsage(ctx context.Context, callID string, u UsageSettlement, cancelled bool) error {
	if callID == "" {
		return errors.New("usage call ID is required")
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.updateLedger(settleCtx, func(a *db.UsageAccount, now time.Time) (db.UsageEntry, error) {
		i := usageIndex(a.Entries, s.runID, callID)
		if i < 0 {
			return db.UsageEntry{}, ErrUsageNotReserved
		}
		old := a.Entries[i]
		next := old
		if cancelled {
			if old.Status == db.UsageCancelled {
				return old, nil
			}
			if old.Status != db.UsageReserved {
				return old, ErrUsageConflict
			}
			next.Status = db.UsageCancelled
			next.Tokens = 0
		} else {
			total, _ := usageSum(u.PromptTokens, u.CompletionTokens)
			if u.Status == UsageUnknown {
				total = max(old.ReservedTokens, total)
			}
			if old.Status == db.UsageCancelled {
				return old, ErrUsageConflict
			}
			if old.Status == u.Status && old.Tokens == total && old.PromptTokens == u.PromptTokens && old.CompletionTokens == u.CompletionTokens {
				return old, nil
			}
			if old.Status != db.UsageReserved && !((old.Status == UsageUnknown || old.Status == UsageEstimated) && u.Status == UsageKnown) {
				return old, ErrUsageConflict
			}
			next.Status = u.Status
			next.Tokens = total
			next.PromptTokens = u.PromptTokens
			next.CompletionTokens = u.CompletionTokens
		}
		next.SettledAt = now.UnixMilli()
		a.Entries[i] = next
		// Detect aggregate overflow before committing rather than wrap into credit.
		if _, err := dailyUsage(a.Entries, now); err != nil {
			return old, err
		}
		return next, nil
	})
	if err != nil {
		return err
	}
	s.entries[callID] = entry
	s.syncMeter()
	return nil
}

// syncMeter publishes a single cumulative total to the existing budget. This
// permits a measured reconciliation to replace an estimate without double
// billing. A terminal budget hit remains sticky in the existing middleware.
func (s *BudgetSession) syncMeter() {
	used, reserved := 0, 0
	reported := false
	for _, e := range s.entries {
		if e.Status == db.UsageReserved {
			reserved = saturatingUsageSum(reserved, e.Tokens)
		} else if e.Status != db.UsageCancelled {
			used = saturatingUsageSum(used, e.Tokens)
			reported = true
		}
	}
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	s.budget.spent = used
	s.budget.reserved = reserved
	s.budget.usageReported = reported
}

func (s *BudgetSession) DailySnapshot(ctx context.Context) (DailyBudgetSnapshot, error) {
	return ReadDailyBudget(ctx, s.ledger, s.owner, 0)
}

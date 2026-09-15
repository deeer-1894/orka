package service

import (
	"context"
	"sync"
)

// BudgetAttempt is a per-attempt legacy UsageSink adapter. It can be installed
// with llm.WithUsageSink(ctx, attempt), so ReportExternalUsage and metered clients
// feed the same ledger. Begin is INSIDE the retry loop and BEFORE dispatch.
// Finish is AFTER all response/stream/GUI callbacks have drained. Pass unknown
// on transport failure or absent usage; AddUsage(0,0) alone is not proof of free
// usage. Never install both this sink and another sink for the same exchange.
type BudgetAttempt struct {
	mu                 sync.Mutex
	session            *BudgetSession
	callID             string
	prompt, completion int
	invalid            error
}

func (s *BudgetSession) BeginUsage(ctx context.Context, callID, source string, reserveTokens int) (*BudgetAttempt, error) {
	if err := s.ReserveUsage(ctx, callID, source, reserveTokens); err != nil {
		return nil, err
	}
	return &BudgetAttempt{session: s, callID: callID}, nil
}

func (a *BudgetAttempt) AddUsage(promptTokens, completionTokens int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.invalid != nil {
		return
	}
	var err error
	if _, err = usageSum(promptTokens, completionTokens); err != nil {
		a.invalid = err
		return
	}
	if a.prompt, err = usageSum(a.prompt, promptTokens); err != nil {
		a.invalid = err
		return
	}
	if a.completion, err = usageSum(a.completion, completionTokens); err != nil {
		a.invalid = err
		return
	}
	_, a.invalid = usageSum(a.prompt, a.completion)
}

// Finish returns persistence/validation errors to the caller. It can be retried
// with the same attempt after storage recovery; a successful duplicate is free.
func (a *BudgetAttempt) Finish(ctx context.Context, status string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.invalid != nil {
		return a.invalid
	} // Keep the existing reservation on bad usage.
	return a.session.SettleUsage(ctx, a.callID, UsageSettlement{Status: status, PromptTokens: a.prompt, CompletionTokens: a.completion})
}

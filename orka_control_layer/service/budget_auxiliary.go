package service

import (
	"context"
	"errors"
)

type budgetAssociation struct{ conversationID, runID string }
type budgetAssociationKey struct{}

// AuxiliaryBudgetContextForRun starts an independent auxiliary usage scope
// linked to a run already authorized by the caller. It never reopens that run's
// execution or changes its status. Every entry retains the association.
func (s *ChatService) AuxiliaryBudgetContextForRun(ctx context.Context, owner, source, conversationID, runID string) (context.Context, context.CancelFunc, error) {
	if conversationID == "" || runID == "" {
		return ctx, nil, errors.New("auxiliary budget requires conversation and run identity")
	}
	if BudgetSessionFrom(ctx) != nil {
		return ctx, nil, errors.New("associated auxiliary budget must be independent of a running budget")
	}
	ctx = context.WithValue(ctx, budgetAssociationKey{}, budgetAssociation{conversationID, runID})
	return s.AuxiliaryBudgetContext(ctx, owner, source)
}

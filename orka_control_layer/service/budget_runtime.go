package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

func (s *ChatService) budgetConfig() config.AgentConfig {
	if s.Cfg == nil {
		return config.AgentConfig{}
	}
	return s.Cfg.Agent
}
func (s *ChatService) budgetLedger() db.UsageLedger {
	if s.UsageLedger != nil {
		return s.UsageLedger
	}
	if s.Msg == nil {
		return db.NewUsageLedger(nil)
	}
	return db.NewUsageLedger(s.Msg.Store)
}

// AuxiliaryBudgetContext is for authenticated, paid API operations that have no
// active chat (followup suggestions and profile probes). Existing parent scopes
// are inherited; otherwise an independently named run shares the owner's
// durable daily ledger. It never touches model selection or credentials.
func (s *ChatService) AuxiliaryBudgetContext(ctx context.Context, owner, source string) (context.Context, context.CancelFunc, error) {
	if owner == "" || source == "" {
		return ctx, nil, errors.New("auxiliary budget requires authenticated owner and source")
	}
	policy := TaskBudgetRequest{}
	ownsScope := BudgetSessionFrom(ctx) == nil

	session, err := NewBudgetSession(ctx, s.budgetConfig(), policy, s.budgetLedger(), owner, "aux_"+messages.NewID())
	if err != nil {
		return ctx, nil, err
	}
	budgetCtx, cancel := session.RunContext(llm.WithAgent(ctx, source))
	return budgetCtx, func() {
		cancel()
		if !ownsScope {
			return
		}
		if err := session.PersistRun(context.WithoutCancel(ctx), session.runID, "done"); err != nil && s.Log != nil {
			s.Log.Error("auxiliary budget snapshot failed", "source", source, "err", err)
		}
	}, nil
}

func applyResumeBudget(a config.AgentConfig, req *ChatRunRequest, saved *runCheckpoint) error {
	if saved == nil {
		return nil
	}
	policy, err := ResolveResumeBudget(a, saved.BudgetPolicy, saved.SpentTokens)
	if err != nil {
		return err
	}
	req.Budget = policy
	// Absence on an old paused checkpoint can still use the persisted original
	// request. A new explicit empty snapshot cannot grant newly supplied tools.
	if saved.toolsRecorded || saved.EnabledTools != nil {
		req.EnabledTools = append([]string(nil), saved.EnabledTools...)
	}
	return nil
}

// prepareRunBudget restores progress and attaches accounting before tool discovery
// or auxiliary calls. Legacy limits are ignored at this compatibility boundary.
func (s *ChatService) prepareRunBudget(ctx context.Context, req *ChatRunRequest) (context.Context, *BudgetSession, context.CancelFunc, error) {
	saved := req.resumeCheckpoint
	if saved == nil && req.resumeFrom != nil {
		saved = req.resumeFrom.Checkpoint
	}
	if req.ResumeKey != "" {
		if s.CP == nil {
			return ctx, nil, nil, errors.New("checkpoint storage unavailable")
		}
		c, err := s.CP.Load(ctx, req.ResumeKey)
		if err != nil {
			return ctx, nil, nil, err
		}
		if c.Meta.UserEmail != req.UserEmail || c.Meta.ConversationID != req.ConversationID {
			return ctx, nil, nil, errors.New("checkpoint identity differs from request")
		}
		saved = &runCheckpoint{}
		if len(c.Runtime) > 0 {
			if err := json.Unmarshal(c.Runtime, saved); err != nil {
				return ctx, nil, nil, fmt.Errorf("invalid runtime checkpoint: %w", err)
			}
		}
		req.resumeCheckpoint = saved
	}
	if err := applyResumeBudget(s.budgetConfig(), req, saved); err != nil {
		return ctx, nil, nil, err
	}
	owner := req.UserEmail
	// Embedded, explicitly ledger-injected executions without a user share one
	// anonymous bucket. Production HTTP authentication supplies a real owner.
	if owner == "" && s.UsageLedger != nil {
		owner = "anonymous"
	}
	session, err := NewBudgetSession(ctx, s.budgetConfig(), req.Budget, s.budgetLedger(), owner, ExecutionID(ctx))
	if err != nil {
		return ctx, nil, nil, err
	}
	if saved != nil {
		restoreCheckpoint(saved, session.Budget(), nil, nil)
	}
	req.Budget = session.Snapshot().Limits
	if err := session.PersistRun(ctx, ExecutionID(ctx), "running"); err != nil {
		return ctx, nil, nil, err
	}
	budgetCtx, cancel := session.RunContext(withBudgetRequestTools(ctx, req.EnabledTools))
	return budgetCtx, session, cancel, nil
}

type budgetRequestToolsKey struct{}

func withBudgetRequestTools(ctx context.Context, tools []string) context.Context {
	return context.WithValue(ctx, budgetRequestToolsKey{}, append([]string(nil), tools...))
}
func budgetRequestTools(ctx context.Context) []string {
	tools, _ := ctx.Value(budgetRequestToolsKey{}).([]string)
	return append([]string(nil), tools...)
}

// Register synchronously before launching auxiliary work, so a final checkpoint
// cannot race a not-yet-started title/digest reservation. The run waits after it
// has scheduled its last auxiliary job; all these jobs have bounded contexts.
type budgetAuxiliaryKey struct{}

func withBudgetAuxiliary(ctx context.Context) context.Context {
	return context.WithValue(ctx, budgetAuxiliaryKey{}, &sync.WaitGroup{})
}
func beginBudgetAuxiliary(ctx context.Context) func() {
	if group, _ := ctx.Value(budgetAuxiliaryKey{}).(*sync.WaitGroup); group != nil {
		group.Add(1)
		return group.Done
	}
	return func() {}
}
func waitBudgetAuxiliary(ctx context.Context) {
	if group, _ := ctx.Value(budgetAuxiliaryKey{}).(*sync.WaitGroup); group != nil {
		group.Wait()
	}
}

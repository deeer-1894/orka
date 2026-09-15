package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/db"
	workflowstate "github.com/orka-oss/orka_control_layer/workflow"
	"github.com/orka-oss/orka_core/messages"
)

// prepareWorkflow creates the conversation, then admits its parent lease before
// persisting execution state. The returned
// release function must live until every child has exited.
func (s *ChatService) prepareWorkflow(ctx context.Context, wf db.Workflow, convID string) (context.Context, func(), db.WorkflowRun, error) {
	if err := workflowstate.Validate(wf.Steps); err != nil {
		return ctx, nil, db.WorkflowRun{}, err
	}
	if s.Msg == nil || s.Msg.Store == nil {
		return ctx, nil, db.WorkflowRun{}, errors.New("workflow storage unavailable")
	}
	if convID == "" {
		convID = messages.NewID()
	}
	if err := s.Msg.Store.EnsureWorkflowConversation(ctx, db.ConversationTable{ConversationID: convID, OwnerEmail: wf.OwnerEmail, Title: "流程 · " + wf.Name, TaskIds: []string{}, CreatedAt: time.Now().UnixMilli()}); err != nil {
		return ctx, nil, db.WorkflowRun{}, err
	}
	parent, release, err := s.AdmitExecution(ctx, wf.OwnerEmail, convID, "")
	if err != nil {
		return ctx, nil, db.WorkflowRun{}, err
	}
	r := workflowstate.NewRun(wf, convID, ExecutionID(parent))
	session, err := NewBudgetSession(parent, s.budgetConfig(), TaskBudgetRequest{}, s.budgetLedger(), wf.OwnerEmail, r.RunID)
	if err != nil {
		release()
		return ctx, nil, r, err
	}
	budgetCtx, cancelBudget := session.RunContext(parent)
	parent = budgetCtx
	releaseExecution := release
	release = func() { cancelBudget(); releaseExecution() }

	if err = s.Msg.Store.SaveWorkflowRun(parent, r); err != nil {
		release()
		return ctx, nil, r, err
	}
	return parent, release, r, nil
}

// StartWorkflow returns only after admission and the initial state are durable.
// Its context owns the entire run; HTTP callers must supply a detached context.
func (s *ChatService) StartWorkflow(ctx context.Context, wf db.Workflow, convID string) (db.WorkflowRun, error) {
	parent, release, r, err := s.prepareWorkflow(ctx, wf, convID)
	if err != nil {
		return r, err
	}
	go func() { defer release(); s.executeWorkflow(parent, wf, r) }()
	return r, nil
}

// RunWorkflow keeps the synchronous caller contract. StartWorkflow additionally
// exposes startup errors and the persisted parent run ID to API callers.
func (s *ChatService) RunWorkflow(ctx context.Context, wf db.Workflow, convID string) string {
	parent, release, r, err := s.prepareWorkflow(ctx, wf, convID)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("start workflow", "error", err)
		}
		return ""
	}
	defer release()
	s.executeWorkflow(parent, wf, r)
	return r.ConversationID
}

func (s *ChatService) executeWorkflow(ctx context.Context, wf db.Workflow, r db.WorkflowRun) {
	engine := workflowstate.Engine{Store: s.Msg.Store, RetryDelay: 3 * time.Second,
		ShouldRun: func(e workflowstate.Execution) bool { return evalRunIf(e.Step.RunIf, e.Prior) },
		RunStep: func(ctx context.Context, e workflowstate.Execution) workflowstate.Result {
			ctx = WithExecutionIdentity(ctx, e.ExecutionID, e.ParentExecutionID)
			out, status := s.runStep(ctx, wf.OwnerEmail, r.ConversationID, e.Step, e.Prior)
			return workflowstate.Result{Status: status, Output: out}
		},
	}
	finished, runErr := engine.Run(ctx, wf, r.ConversationID, r.RunID)
	if runErr != nil && s.Log != nil {
		s.Log.Error("workflow persistence", "run_id", r.RunID, "error", runErr)
	}
	if session := BudgetSessionFrom(ctx); session != nil {
		if err := session.PersistRun(ctx, r.RunID, finished.Status); err != nil && s.Log != nil {
			s.Log.Error("workflow budget snapshot failed", "run_id", r.RunID, "error", err)
		}
	}
	if s.OnEvent != nil {
		s.OnEvent(wf.OwnerEmail, "run")
	}
}

// runStep preserves the authoritative status. The workflow engine owns retries
// and gives every attempt a distinct execution identity and durable state.
func (s *ChatService) runStep(ctx context.Context, owner, convID string, st db.WorkflowStep, prior map[string]string) (string, string) {
	var chat, stream strings.Builder
	status := s.Run(ctx, ChatRunRequest{Message: substitute(st.Prompt, prior), ConversationID: convID, UserEmail: owner, Trigger: "workflow"}, func(m messages.Message) {
		if m.Role != messages.RoleAssistant {
			return
		}
		switch m.Type {
		case messages.EventChat:
			chat.WriteString(m.Content)
		case messages.EventStream:
			stream.WriteString(m.Content)
		}
	})
	out := strings.TrimSpace(chat.String())
	if out == "" {
		out = strings.TrimSpace(stream.String())
	}
	return out, status
}

func normalizeDAG(steps []db.WorkflowStep) []db.WorkflowStep { return workflowstate.Normalize(steps) }

// evalRunIf evaluates a guard like `research contains FOUND` against prior step
// outputs. Supported ops: contains, !contains, ==, !=. Empty/unparseable guards
// run (fail-open) so a typo never silently drops a step.
func evalRunIf(expr string, outputs map[string]string) bool {
	expr = strings.TrimSpace(substitute(expr, outputs))
	if expr == "" {
		return true
	}
	for _, op := range []string{"!contains", "contains", "==", "!="} {
		i := strings.Index(expr, " "+op+" ")
		if i < 0 {
			continue
		}
		left := strings.TrimSpace(expr[:i])
		right := unquote(strings.TrimSpace(expr[i+len(op)+2:]))
		if v, ok := outputs[left]; ok { // bare step name → its output
			left = v
		}
		switch op {
		case "contains":
			return strings.Contains(left, right)
		case "!contains":
			return !strings.Contains(left, right)
		case "==":
			return strings.TrimSpace(left) == right
		case "!=":
			return strings.TrimSpace(left) != right
		}
	}
	return true
}

// substitute replaces {{step_name}} with that step's output (truncated).
func substitute(s string, outputs map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	for name, out := range outputs {
		if len(out) > 4000 {
			out = out[:4000]
		}
		s = strings.ReplaceAll(s, "{{"+name+"}}", out)
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

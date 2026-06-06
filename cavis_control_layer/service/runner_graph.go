package service

import (
	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/messages"
)

// GraphRunner is the deterministic execution mode. It runs the same middleware
// chain as the ADK runner but as a fixed, ordered "graph" of nodes and records
// the executed node sequence in Vars["graph_trace"] for reproducibility and
// eval. It shares RunContext/Middleware with the ADK runner; the only
// difference is the explicit, model-independent ordering and the recorded trace
// — which is what makes it suitable for compliance/审批 flows that must be
// reproducible.
type GraphRunner struct {
	middlewares []agent.Middleware
}

// NewGraphRunner builds a graph runner over the chain.
func NewGraphRunner(mws ...agent.Middleware) *GraphRunner {
	return &GraphRunner{middlewares: mws}
}

// VarGraphTrace holds the ordered node names executed in graph mode.
const VarGraphTrace = "graph_trace"

func (r *GraphRunner) Run(rc *agent.RunContext) error { return r.exec(rc) }

func (r *GraphRunner) ResumeWithParams(rc *agent.RunContext, resumeKey, userInput string) error {
	rc.Interrupt = nil
	if rc.Vars == nil {
		rc.Vars = map[string]any{}
	}
	rc.Vars["resume_key"] = resumeKey
	if userInput != "" {
		rc.Messages = append(rc.Messages, messages.Chat(messages.RoleUser, userInput, rc.Meta))
	}
	return r.exec(rc)
}

func (r *GraphRunner) exec(rc *agent.RunContext) error {
	if rc.Vars == nil {
		rc.Vars = map[string]any{}
	}
	trace := traceOf(rc)
	noop := func(*agent.RunContext) error { return nil }
	for rc.Cursor < len(r.middlewares) {
		if err := rc.Ctx.Err(); err != nil {
			rc.Vars[VarGraphTrace] = trace
			return err
		}
		mw := r.middlewares[rc.Cursor]
		trace = append(trace, mw.Name())
		if err := mw.Handle(rc, noop); err != nil {
			rc.Vars[VarGraphTrace] = trace
			return err
		}
		if rc.Interrupt != nil {
			rc.Vars[VarGraphTrace] = trace
			return nil
		}
		rc.Cursor++
	}
	rc.Vars[VarGraphTrace] = trace
	return nil
}

func traceOf(rc *agent.RunContext) []string {
	if v, ok := rc.Vars[VarGraphTrace].([]string); ok {
		return v
	}
	return nil
}

// RunnerForMode returns the runner for the given RUN_MODE ("graph" -> deterministic
// GraphRunner, anything else -> the model-driven ADK DefaultRunner).
func RunnerForMode(mode string, mws ...agent.Middleware) agent.Runner {
	if mode == "graph" {
		return NewGraphRunner(mws...)
	}
	return agent.NewDefaultRunner(mws...)
}

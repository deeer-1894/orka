package service

import (
	"context"
	"sync"
)

// confirm_args.go — keep the arguments of a call a human approved.
//
// StreamEinoRun records a tool call's arguments by watching the ASSISTANT turn
// that requested it, keyed by tool-call id. That turn is emitted once, in the
// run that made the request. A danger tool interrupts there: the run is
// checkpointed, the user approves, and the work finishes in a SECOND run whose
// map starts empty — so the tool result arrives with no arguments to attach and
// the audit record reads `args: null`.
//
// It lands on exactly the calls that were gated for being dangerous. Measured
// across every conversation that used the confirm gate: 16 of 141 danger-tool
// calls lost their arguments, and 12 of those 14 landed within two minutes of an
// approval. The one thing the record must answer — what did I approve, and what
// then ran — was the one thing missing.
//
// The gate itself has the arguments on the way back in (it calls the underlying
// tool with them), so it publishes them here and the event path reads them back.

// confirmedArgs holds the arguments of calls this run executed after approval,
// keyed by tool name. One pending confirmation per run makes the name enough to
// address it, and the gate does not see the tool-call id.
type confirmedArgs struct {
	mu     sync.Mutex
	byTool map[string]map[string]any
}

func newConfirmedArgs() *confirmedArgs {
	return &confirmedArgs{byTool: map[string]map[string]any{}}
}

func (c *confirmedArgs) put(tool string, args map[string]any) {
	if c == nil || tool == "" || args == nil {
		return
	}
	c.mu.Lock()
	c.byTool[tool] = args
	c.mu.Unlock()
}

// take returns the arguments recorded for a tool and forgets them, so a second
// call of the same tool cannot inherit the first one's record.
func (c *confirmedArgs) take(tool string) map[string]any {
	if c == nil || tool == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	args, ok := c.byTool[tool]
	if !ok {
		return nil
	}
	delete(c.byTool, tool)
	return args
}

type confirmedArgsKey struct{}

func withConfirmedArgs(ctx context.Context, c *confirmedArgs) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, confirmedArgsKey{}, c)
}

func confirmedArgsFrom(ctx context.Context) *confirmedArgs {
	c, _ := ctx.Value(confirmedArgsKey{}).(*confirmedArgs)
	return c
}

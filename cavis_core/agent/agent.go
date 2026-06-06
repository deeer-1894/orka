// Package agent defines the ADK-style runtime abstractions: RunContext,
// Middleware, BaseTool and Runner, plus a default cursor-resumable runner.
package agent

import (
	"context"

	"github.com/cavis-oss/cavis_core/messages"
)

// RunContext is the shared state middlewares read/write as they execute.
type RunContext struct {
	Ctx       context.Context
	Messages  []messages.Message      // full history
	Tools     []BaseTool              // available tools
	Cursor    int                     // current middleware position (for resume)
	Vars      map[string]any          // intermediate variables
	Interrupt *Interrupt              // non-nil requests an interrupt
	Send      func(messages.Message)  // unified emit sink (injected)
	Meta      messages.Meta           // session metadata stamped on emitted msgs
}

// Interrupt signals the runner to stop and persist a checkpoint.
type Interrupt struct {
	Reason  string
	Clarify *messages.ClarifyMessage
}

// Middleware is one unit in the pipeline. It may continue (return nil) or
// request an interrupt by setting rc.Interrupt.
type Middleware interface {
	Name() string
	Handle(rc *RunContext, next func(*RunContext) error) error
}

// BaseTool is the unified tool interface (local or remote MCP).
type BaseTool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Invoke(ctx context.Context, args map[string]any) (string, error)
}

// Runner assembles middlewares and supports Run / Resume.
type Runner interface {
	Run(rc *RunContext) error
	ResumeWithParams(rc *RunContext, resumeKey string, userInput string) error
}

// Emit sends a message through the injected sink (no-op if unset).
func (rc *RunContext) Emit(m messages.Message) {
	if rc.Send != nil {
		rc.Send(m)
	}
}

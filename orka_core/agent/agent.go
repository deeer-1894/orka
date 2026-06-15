// Package agent defines the ADK-style runtime abstractions: RunContext,
// Middleware, BaseTool and Runner, plus a default cursor-resumable runner.
package agent

import (
	"context"

	"github.com/orka-oss/orka_core/messages"
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

// ---- Vars accessors ----
//
// Vars is an untyped bag shared across middlewares. These helpers centralize the
// nil-map guard and use comma-ok assertions so a wrong/missing key degrades to a
// zero value instead of panicking. Typed, named accessors for specific keys live
// in the middlewares package (which can see the llm/messages types).

// Put sets a var, lazily allocating the map.
func (rc *RunContext) Put(key string, v any) {
	if rc.Vars == nil {
		rc.Vars = map[string]any{}
	}
	rc.Vars[key] = v
}

// Str returns the string var at key, or "" if absent/mistyped.
func (rc *RunContext) Str(key string) string {
	s, _ := rc.Vars[key].(string)
	return s
}

// IntVar returns the int var at key, or 0 if absent/mistyped.
func (rc *RunContext) IntVar(key string) int {
	n, _ := rc.Vars[key].(int)
	return n
}

// StrSlice returns the []string var at key, or nil if absent/mistyped.
func (rc *RunContext) StrSlice(key string) []string {
	s, _ := rc.Vars[key].([]string)
	return s
}

// Has reports whether key is present.
func (rc *RunContext) Has(key string) bool {
	_, ok := rc.Vars[key]
	return ok
}

package agent

import "github.com/orka-oss/orka_core/messages"

// DefaultRunner executes the middleware chain linearly. Middlewares are
// cursor-resumable: on Interrupt the runner stops and keeps Cursor pointing at
// the interrupting middleware, so Resume restarts from that exact position.
//
// The `next` parameter of Middleware.Handle is a no-op continuation in this
// runner; the linear model is what makes cursor-based resume tractable. It is
// reserved for future onion-style composition.
type DefaultRunner struct {
	middlewares []Middleware
}

// NewDefaultRunner builds a runner over the given middleware chain.
func NewDefaultRunner(mws ...Middleware) *DefaultRunner {
	return &DefaultRunner{middlewares: mws}
}

func noop(*RunContext) error { return nil }

// Run executes from rc.Cursor to the end (or until an interrupt).
func (r *DefaultRunner) Run(rc *RunContext) error {
	return r.runFrom(rc)
}

// ResumeWithParams clears the prior interrupt, injects the user's reply as a
// new user message, and continues from the saved cursor.
func (r *DefaultRunner) ResumeWithParams(rc *RunContext, resumeKey, userInput string) error {
	rc.Interrupt = nil
	if rc.Vars == nil {
		rc.Vars = map[string]any{}
	}
	rc.Vars["resume_key"] = resumeKey
	if userInput != "" {
		rc.Messages = append(rc.Messages, messages.Chat(messages.RoleUser, userInput, rc.Meta))
	}
	return r.runFrom(rc)
}

func (r *DefaultRunner) runFrom(rc *RunContext) error {
	if rc.Vars == nil {
		rc.Vars = map[string]any{}
	}
	for rc.Cursor < len(r.middlewares) {
		if err := rc.Ctx.Err(); err != nil {
			return err // honor cancellation (e.g. /chat/kill)
		}
		mw := r.middlewares[rc.Cursor]
		if err := mw.Handle(rc, noop); err != nil {
			return err
		}
		if rc.Interrupt != nil {
			return nil // stop; keep Cursor at the interrupting middleware
		}
		rc.Cursor++
	}
	return nil
}

package agent

import (
	"context"

	"github.com/orka-oss/orka_core/messages"
)

// emitKey carries a side-channel emit function on the context so tools that only
// receive a context (BaseTool.Invoke) can stream events (e.g. a GUI agent
// surfacing browser events) into the same SSE sink as the run.
type emitKey struct{}

// WithEmit attaches an emit function to ctx.
func WithEmit(ctx context.Context, fn func(messages.Message)) context.Context {
	return context.WithValue(ctx, emitKey{}, fn)
}

// EmitFrom returns the emit function from ctx, or nil if absent.
func EmitFrom(ctx context.Context) func(messages.Message) {
	if fn, ok := ctx.Value(emitKey{}).(func(messages.Message)); ok {
		return fn
	}
	return nil
}

package llm

import "context"

// A reasoning sink lets the streaming client surface a reasoning model's live
// "thinking" tokens (OpenAI `reasoning_content` deltas) as a side channel,
// separate from the answer stream. The chat runtime installs it on the context
// with the right session metadata; the client just forwards deltas.

type reasoningKey struct{}

// WithReasoningSink attaches a reasoning-delta callback to the context.
func WithReasoningSink(ctx context.Context, fn func(string)) context.Context {
	return context.WithValue(ctx, reasoningKey{}, fn)
}

// ReasoningSinkFrom returns the installed sink, or nil.
func ReasoningSinkFrom(ctx context.Context) func(string) {
	fn, _ := ctx.Value(reasoningKey{}).(func(string))
	return fn
}

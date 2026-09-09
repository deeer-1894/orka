package llm

import "context"

// turn.go — reporting the SHAPE of a completed model turn, not just its cost.
//
// A run ended silently and was filed as a success: 693 seconds, one model call,
// zero tool calls, no file produced. The model had spent the whole call
// composing an SVG inside its reasoning — 82,903 characters of draft — hit the
// provider's output ceiling, and the response ended mid-token. Nothing
// downstream could tell that turn apart from a model that had finished
// speaking, because the only thing reported about a call was what it cost.
//
// finish_reason was already carried all the way into the eino adapter and read
// by nobody: `grep FinishReason service/` matched zero lines. A response that
// stopped because it ran out of room is not an answer, and the difference is
// one string.

// Turn describes a completed model exchange. Everything here is decided by the
// provider, so it is the one account of the turn that cannot be edited by later
// context management.
type Turn struct {
	// Agent is who made the call. Sub-agents share the run's context, so without
	// it a delegate's truncated turn is indistinguishable from the orchestrator's
	// — and only the orchestrator's ends the run.
	Agent string
	// FinishReason is the provider's own word for why generation stopped.
	// "length" means truncated: there was more to say.
	FinishReason string
	// ToolCalls is how many calls the turn requested. Zero on a turn that only
	// talked — which, combined with a truncated finish, is the signature of a
	// model that thought instead of acting.
	ToolCalls int
	// ContentRunes and ReasoningRunes size what the model said versus what it
	// only thought. A turn that is all reasoning and no content produced nothing
	// the user asked for.
	ContentRunes   int
	ReasoningRunes int
}

// Truncated reports whether the provider cut the turn short.
func (t Turn) Truncated() bool { return t.FinishReason == "length" }

// TurnSink receives every completed turn of a run.
type TurnSink interface {
	ObserveTurn(Turn)
}

type turnSinkKey struct{}

// WithTurnSink attaches a run's turn observer to a context.
func WithTurnSink(ctx context.Context, s TurnSink) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, turnSinkKey{}, s)
}

func turnSinkFrom(ctx context.Context) TurnSink {
	s, _ := ctx.Value(turnSinkKey{}).(TurnSink)
	return s
}

// observeTurn reports a completed exchange. Called from the same choke point as
// usage accounting, for the same reason: the provider exchange happens once and
// cannot be rewritten afterwards.
func observeTurn(ctx context.Context, resp Response) {
	s := turnSinkFrom(ctx)
	if s == nil {
		return
	}
	s.ObserveTurn(Turn{
		Agent:          AgentFromContext(ctx),
		FinishReason:   resp.FinishReason,
		ToolCalls:      len(resp.ToolCalls),
		ContentRunes:   len([]rune(resp.Content)),
		ReasoningRunes: len([]rune(resp.Reasoning)),
	})
}

package llm

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// timing.go — how long a model call actually took, measured at the call.
//
// Every latency figure in this codebase so far has been inferred from something
// adjacent: gaps between tool events, or intervals between middleware probes.
// Both conflate three different waits — the model generating, the turn's tools
// executing, and an orchestrator blocked on a delegate — and the probe cannot
// even separate concurrent delegates, because they share one agent name. Three
// successive attempts to attribute a slow run from those signals produced three
// wrong answers.
//
// This measures the one thing that is unambiguous: the duration of the HTTP
// exchange with the provider, with the model, the token counts and the caller's
// agent alongside it. Anything left over after subtracting these is, by
// construction, not the model.
//
// Off unless ORKA_LLM_DEBUG=1: one line per model call.

const llmDebugEnv = "ORKA_LLM_DEBUG"

func timingEnabled() bool {
	v := os.Getenv(llmDebugEnv)
	return v == "1" || strings.EqualFold(v, "true")
}

// agentKey carries the calling agent's name from EinoModel down to the timing
// decorator. It has to travel on the context rather than be resolved from run
// meta: eino invokes the model from inside its own graph, and concurrent
// delegates of one kind share a single name, so the model instance is the only
// place that knows which agent a call belongs to.
type agentKey struct{}

func withAgent(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, agentKey{}, name)
}

// AgentFromContext reports the agent a model call belongs to, or "".
func AgentFromContext(ctx context.Context) string {
	s, _ := ctx.Value(agentKey{}).(string)
	return s
}

// Timed decorates a Client so each call reports its own duration. Wrap INSIDE
// the limiter and outside nothing — the point is to time the provider exchange,
// not the queueing in front of it, which is what the limiter's own accounting
// would measure.
type Timed struct {
	Client
	agentOf func(context.Context) string
}

// NewTimedFromEnv returns c unchanged unless ORKA_LLM_DEBUG is set, so the
// decorator costs nothing in production and needs no call-site changes.
// agentOf resolves the calling agent for attribution; nil is fine.
func NewTimedFromEnv(c Client, agentOf func(context.Context) string) Client {
	if !timingEnabled() {
		return c
	}
	return &Timed{Client: c, agentOf: agentOf}
}

var llmStats struct {
	mu sync.Mutex
	n  int
	d  time.Duration
}

func (t *Timed) label(ctx context.Context) string {
	if a := AgentFromContext(ctx); a != "" {
		return a
	}
	if t.agentOf == nil {
		return ""
	}
	return t.agentOf(ctx)
}

func (t *Timed) log(ctx context.Context, req Request, resp Response, d time.Duration, streamed bool, err error) {
	llmStats.mu.Lock()
	llmStats.n++
	llmStats.d += d
	n, total := llmStats.n, llmStats.d
	llmStats.mu.Unlock()

	args := []any{
		"model", req.Model,
		"agent", t.label(ctx),
		"seconds", d.Round(time.Millisecond / 10).Seconds(),
		"streamed", streamed,
		"in", resp.Usage.PromptTokens,
		"out", resp.Usage.CompletionTokens,
		"reasoning", resp.Usage.ReasoningTokens,
		// A running total, so a finished run's model time can be read off the
		// last line instead of summing the log by hand.
		"calls_so_far", n,
		"model_seconds_so_far", total.Round(time.Second).Seconds(),
	}
	if err != nil {
		args = append(args, "err", err.Error())
	}
	slog.Default().Info("llm call", args...)
}

func (t *Timed) Chat(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := t.Client.Chat(ctx, req)
	t.log(ctx, req, resp, time.Since(start), false, err)
	return resp, err
}

// ChatStream reports the FULL exchange, not time-to-first-token. A streamed call
// still blocks its agent until the last token, so that is the number the wall
// clock is made of.
func (t *Timed) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	sc, ok := t.Client.(StreamingClient)
	if !ok {
		return t.Chat(ctx, req)
	}
	start := time.Now()
	resp, err := sc.ChatStream(ctx, req, onDelta)
	t.log(ctx, req, resp, time.Since(start), true, err)
	return resp, err
}

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

// WithAgent labels model calls made directly on a Client, rather than through an
// EinoModel — the auxiliary work that has no agent of its own (summarizing,
// titling, digesting, the no-tool fast path). They are easy to forget and not
// small: six unlabelled calls once accounted for 36% of a run's model time, more
// than the orchestrator's own.
func WithAgent(ctx context.Context, name string) context.Context { return withAgent(ctx, name) }

// AgentFromContext reports the agent a model call belongs to, or "".
func AgentFromContext(ctx context.Context) string {
	s, _ := ctx.Value(agentKey{}).(string)
	return s
}

// UsageSink receives what a completed model call actually cost. It exists
// because the alternative — recomputing a run's spend from its message list —
// is not measuring spend at all.
//
// The run budget used to sum ResponseMeta.Usage over the messages it was handed
// each cycle. That is a reading of "usage still recorded in the current window",
// and the window is continuously rewritten by the very context management this
// system relies on: reduction clears tool results, and summarization collapses
// the history to a system message plus one summary, taking the assistant
// messages that carried the usage with it. So the total kept resetting. Measured
// on one run: 2,293,602 tokens actually billed against an 800k cap that never
// fired, because the number it compared was never the number that was spent.
//
// The provider exchange is the one place a cost is unambiguous, happens once,
// and cannot be edited afterwards. Retries and failovers each land here
// separately, which is correct — every one of them is billed.
type UsageSink interface {
	AddUsage(promptTokens, completionTokens int)
}

type usageSinkKey struct{}

// WithUsageSink attaches the run's accountant to a context. Model calls made
// under it report their cost as they complete.
func WithUsageSink(ctx context.Context, s UsageSink) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, usageSinkKey{}, s)
}

func usageSinkFrom(ctx context.Context) UsageSink {
	s, _ := ctx.Value(usageSinkKey{}).(UsageSink)
	return s
}

// Metered decorates a Client so every call reports its cost, and — when
// ORKA_LLM_DEBUG is set — its duration and token split.
//
// Wrap INSIDE the limiter: the point is to measure the provider exchange, not
// the queueing in front of it, so queue time stays visible as the difference
// between a call's spacing and its logged duration.
type Metered struct {
	Client
	agentOf func(context.Context) string
}

// NewMetered always wraps. Reporting usage is load-bearing — the run's cost
// ceiling depends on it — so unlike the logging it cannot be conditional on a
// debug switch. agentOf resolves the calling agent for attribution; nil is fine.
func NewMetered(c Client, agentOf func(context.Context) string) Client {
	return &Metered{Client: c, agentOf: agentOf}
}

// Timed is the previous name, kept so external callers still compile.
type Timed = Metered

var llmStats struct {
	mu sync.Mutex
	n  int
	d  time.Duration
}

// report books the call's cost first, then logs. The booking is unconditional;
// the logging is not.
func (t *Metered) report(ctx context.Context, req Request, resp Response, d time.Duration, streamed bool, err error) {
	if s := usageSinkFrom(ctx); s != nil {
		s.AddUsage(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}
	if timingEnabled() {
		t.log(ctx, req, resp, d, streamed, err)
	}
}

func (t *Metered) label(ctx context.Context) string {
	if a := AgentFromContext(ctx); a != "" {
		return a
	}
	if t.agentOf == nil {
		return ""
	}
	return t.agentOf(ctx)
}

func (t *Metered) log(ctx context.Context, req Request, resp Response, d time.Duration, streamed bool, err error) {
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

func (t *Metered) Chat(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	resp, err := t.Client.Chat(ctx, req)
	t.report(ctx, req, resp, time.Since(start), false, err)
	return resp, err
}

// ChatStream reports the FULL exchange, not time-to-first-token. A streamed call
// still blocks its agent until the last token, so that is the number the wall
// clock is made of.
func (t *Metered) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	sc, ok := t.Client.(StreamingClient)
	if !ok {
		return t.Chat(ctx, req)
	}
	start := time.Now()
	resp, err := sc.ChatStream(ctx, req, onDelta)
	t.report(ctx, req, resp, time.Since(start), true, err)
	return resp, err
}

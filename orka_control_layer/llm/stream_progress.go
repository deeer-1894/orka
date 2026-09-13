package llm

import (
	"context"
	"log/slog"
	"time"
)

// streamProgressEvery is how often a still-running stream reports what it has
// produced. A minute is dense enough to watch a thirteen-minute call unfold and
// sparse enough that an ordinary turn never logs at all.
const streamProgressEvery = time.Minute

// streamProgress reports a long stream's output as it accumulates. Off unless
// ORKA_LLM_DEBUG is set, like the per-call timing line it complements.
//
// The split it logs is the question every long call raises: is the model
// reasoning (reasoning_chars), writing its answer in the open (content_chars),
// or emitting a tool call (tool_arg_chars)? Characters, not tokens — the token
// counts only exist in the final chunk, which is exactly what a stalled or
// cancelled call never sends.
type streamProgress struct {
	on    bool
	ctx   context.Context
	model string
	start time.Time
	last  time.Time
}

func newStreamProgress(ctx context.Context, model string) *streamProgress {
	now := time.Now()
	return &streamProgress{on: timingEnabled(), ctx: ctx, model: model, start: now, last: now}
}

func (p *streamProgress) tick(reasoningChars, contentChars, toolChars int) {
	if !p.on || time.Since(p.last) < streamProgressEvery {
		return
	}
	p.last = time.Now()
	slog.Default().Info("llm stream progress",
		"model", p.model, "agent", AgentFromContext(p.ctx),
		"elapsed_s", int(time.Since(p.start).Seconds()),
		"reasoning_chars", reasoningChars, "content_chars", contentChars, "tool_arg_chars", toolChars)
}

// aborted records what a failed stream had produced. Logged whether or not
// debugging is on: an aborted multi-minute call is rare and expensive, and the
// breakdown is the only record of what the time was spent on.
func (p *streamProgress) aborted(reasoningChars, contentChars, toolChars, toolCalls int, err error) {
	slog.Default().Warn("llm stream aborted",
		"model", p.model, "agent", AgentFromContext(p.ctx),
		"elapsed_s", int(time.Since(p.start).Seconds()),
		"reasoning_chars", reasoningChars, "content_chars", contentChars,
		"tool_arg_chars", toolChars, "tool_calls_started", toolCalls, "err", err.Error())
}

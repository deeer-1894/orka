package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// eino_context_probe.go — measuring what the reducer is deciding on.
//
// The reduction layer is tuned by two numbers (clearAboveTokens and the
// ClearAtLeastTokens floor) that were chosen from token counts measured at the
// PROVIDER — cumulative billed tokens across a run. That is a cost figure, not a
// context size: every call re-bills the whole prompt, so the two diverge fast.
// Nothing so far has reported the number the reducer actually tests, which left
// the thresholds unfalsifiable — a 424k-token run offloaded exactly one tool
// result and nobody could say whether it came close to the clear threshold or
// never approached it.
//
// This probe reports that number, on both sides of the reduction pass, so the
// question becomes arithmetic. It is off unless ORKA_CTX_DEBUG=1: it logs once
// per model call, which is the right density for tuning and the wrong one for
// production.
//
// It deliberately does NOT supply reduction.Config.TokenCounter. Substituting
// the counter would make the log exact, and would also change what gets cleared
// — the wrong trade for a diagnostic. It instead reimplements eino's message
// accounting (defaultTokenCounter: bytes of a rendered message over 4) so the
// figure is comparable with the threshold to within the tool-schema offset,
// which is a constant per agent and reported separately.

const ctxDebugEnv = "ORKA_CTX_DEBUG"

func ctxProbeEnabled() bool {
	v := os.Getenv(ctxDebugEnv)
	return v == "1" || strings.EqualFold(v, "true")
}

// ctxProbePair returns the before/after halves of one probe. They share state so
// a single log line can carry both sides and their delta; returns nils when
// disabled, and appending a nil middleware is what callers must avoid.
func ctxProbePair(label string) (adk.ChatModelAgentMiddleware, adk.ChatModelAgentMiddleware) {
	if !ctxProbeEnabled() {
		return nil, nil
	}
	st := &ctxProbeState{label: label}
	return &ctxProbe{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, st: st, post: false},
		&ctxProbe{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, st: st, post: true}
}

type ctxProbeState struct {
	label string

	mu      sync.Mutex
	preTok  int64
	preMsgs int
	seen    bool
}

type ctxProbe struct {
	*adk.BaseChatModelAgentMiddleware
	st   *ctxProbeState
	post bool
}

func (p *ctxProbe) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	tok, msgs, biggest, biggestName := probeCount(state.Messages)
	p.st.mu.Lock()
	defer p.st.mu.Unlock()

	if !p.post {
		p.st.preTok, p.st.preMsgs, p.st.seen = tok, msgs, true
		return ctx, state, nil
	}
	if !p.st.seen {
		return ctx, state, nil // post without a pre: nothing to compare against
	}
	// Reported together so one line answers "how big was it, what did reduction
	// take, and how far is that from the threshold that would have taken more".
	slog.Default().Info("ctx probe",
		"agent", p.st.label,
		"msgs_pre", p.st.preMsgs, "msgs_post", msgs,
		"tok_pre", p.st.preTok, "tok_post", tok,
		"tok_reclaimed", p.st.preTok-tok,
		"clear_threshold", int64(clearAboveTokens),
		"pct_of_threshold", pct(p.st.preTok, clearAboveTokens),
		"tools_schema_tok", schemaTokens(state.ToolInfos),
		"biggest_tool_msg_tok", biggest, "biggest_tool", biggestName,
	)
	p.st.seen = false
	return ctx, state, nil
}

// probeCount mirrors eino's defaultTokenCounter for messages: the rendered
// message over four bytes. Reimplemented rather than imported because eino keeps
// it unexported; it must track that function, not improve on it, or the number
// stops being comparable with the threshold.
//
// Note this counts BYTES, so CJK (3 bytes per character) is scored at about
// 0.75 per character where a real tokenizer charges roughly 1 — a Chinese-heavy
// context is therefore further along than the threshold arithmetic suggests.
func probeCount(msgs []*schema.Message) (tokens int64, count int, biggestToolTok int64, biggestTool string) {
	for _, m := range msgs {
		if m == nil {
			continue
		}
		count++
		var sb strings.Builder
		sb.WriteString(string(m.Role))
		sb.WriteString("\n")
		sb.WriteString(m.ReasoningContent)
		sb.WriteString("\n")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
		if m.Role == schema.Assistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				sb.WriteString(tc.Function.Name)
				sb.WriteString("\n")
				sb.WriteString(tc.Function.Arguments)
			}
		}
		for _, mc := range m.UserInputMultiContent {
			if mc.Type == schema.ChatMessagePartTypeText {
				sb.WriteString(mc.Text)
				sb.WriteString("\n")
			}
		}
		for _, mc := range m.AssistantGenMultiContent {
			if mc.Type == schema.ChatMessagePartTypeText {
				sb.WriteString(mc.Text)
				sb.WriteString("\n")
			}
		}
		n := int64(len(sb.String()) / 4)
		tokens += n
		// The single biggest tool result is the one worth naming: it is what
		// truncation would have caught, and what clearing would reclaim first.
		if m.Role == schema.Tool && n > biggestToolTok {
			biggestToolTok, biggestTool = n, m.ToolName
		}
	}
	return tokens, count, biggestToolTok, biggestTool
}

// schemaTokens is the tool-table cost carried on every call. eino marshals with
// sonic and this uses encoding/json, so it is close but not identical — it is
// reported apart from the message total for exactly that reason.
func schemaTokens(tools []*schema.ToolInfo) int64 {
	var n int64
	for _, tl := range tools {
		if tl == nil {
			continue
		}
		b, err := json.Marshal(tl)
		if err != nil {
			continue
		}
		n += int64(len(b) / 4)
	}
	return n
}

func pct(v int64, of int) int {
	if of == 0 {
		return 0
	}
	return int(v * 100 / int64(of))
}

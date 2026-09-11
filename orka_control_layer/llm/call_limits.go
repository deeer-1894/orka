package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CallLimits bounds one generation and its single length-recovery attempt.
// The service chooses policy; the adapter owns transport and response integrity.
// A zero policy preserves the adapter's original streaming behavior.
type CallLimits struct {
	FirstMaxTokens int
	MaxTokens      int
	Timeout        time.Duration
	// OnLengthRetry clears transient presentation before a replacement attempt.
	OnLengthRetry func(context.Context)
}

type callLimitError struct {
	reason string
	cause  error
}

func (e *callLimitError) Error() string {
	return fmt.Sprintf("model call limit: %s; split the work into smaller steps", e.reason)
}
func (e *callLimitError) Unwrap() error { return e.cause }

// IsCallLimit identifies exhausted local limits, which must not be multiplied
// by outer retries or model-tier failover.
func IsCallLimit(err error) bool { var target *callLimitError; return errors.As(err, &target) }

// WithCallLimits returns an independently configured copy, safe to bind/reuse.
func (m *EinoModel) WithCallLimits(limits CallLimits) *EinoModel {
	cp := *m
	cp.limits = limits
	return &cp
}
func (l CallLimits) enabled() bool { return l.MaxTokens > 0 || l.FirstMaxTokens > 0 || l.Timeout > 0 }

func (l CallLimits) apply(req Request) Request {
	cap := l.MaxTokens
	first := true
	for _, msg := range req.Messages {
		if msg.Role == RoleAssistant || msg.Role == RoleTool {
			first = false
			break
		}
	}
	if first && l.FirstMaxTokens > 0 && (cap <= 0 || l.FirstMaxTokens < cap) {
		cap = l.FirstMaxTokens
	}
	if cap > 0 && (req.MaxTokens <= 0 || req.MaxTokens > cap) {
		req.MaxTokens = cap
	}
	return req
}

// limitedResponse buffers content at the adapter boundary so a rejected
// completion can never execute partial tools or become a successful final answer.
// Streaming clients still provide live reasoning via their existing sink, and
// the existing metered client accounts every provider-reported attempt.
func (m *EinoModel) limitedResponse(ctx context.Context, req Request, stream bool) (Response, error) {
	ctx = withAgent(ctx, m.agent)
	callCtx := ctx
	if m.limits.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, m.limits.Timeout)
		defer cancel()
	}
	req = m.limits.apply(req)
	for attempt := 0; attempt < 2; attempt++ {
		var resp Response
		var err error
		if sc, ok := m.client.(StreamingClient); stream && ok {
			resp, err = sc.ChatStream(callCtx, req, func(string) {})
		} else {
			resp, err = m.client.Chat(callCtx, req)
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if callCtx.Err() != nil {
			return Response{}, &callLimitError{reason: "generation deadline exceeded", cause: callCtx.Err()}
		}
		if err != nil {
			return Response{}, err
		}
		if resp.FinishReason != "length" {
			return resp, nil
		}
		if attempt == 1 {
			return Response{}, &callLimitError{reason: "output truncated twice"}
		}
		if m.limits.OnLengthRetry != nil {
			m.limits.OnLengthRetry(ctx)
		}
		messages := make([]ChatMessage, len(req.Messages), len(req.Messages)+1)
		copy(messages, req.Messages)
		req.Messages = append(messages, ChatMessage{Role: RoleUser, Content: "Your previous generation exceeded the per-call output limit and was discarded. No tool calls from it were executed. Return one small next action with complete arguments, or a concise answer. Do not draft the entire project in one response; split large files across steps."})
	}
	panic("unreachable")
}

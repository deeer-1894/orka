package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CallLimits bounds one generation and its single response-recovery attempt.
// The service chooses policy; the adapter owns transport and response integrity.
// A zero policy preserves the adapter's original streaming behavior.
type CallLimits struct {
	// ForContext resolves an immutable run policy without mutating a shared model.
	ForContext     func(context.Context, string) CallLimits
	FirstMaxTokens int
	MaxTokens      int
	Timeout        time.Duration
	// ReasoningEffort resolves service policy against the actual requested model,
	// including per-call overrides. Nil preserves the provider default.
	ReasoningEffort func(model string) string
	// OnResponseRetry clears transient presentation before a replacement attempt.
	OnResponseRetry func(context.Context)
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
func (l CallLimits) enabled() bool {
	return l.MaxTokens > 0 || l.FirstMaxTokens > 0 || l.Timeout > 0 || l.ReasoningEffort != nil
}

func (l CallLimits) apply(req Request) Request {
	if req.ReasoningEffort == "" && l.ReasoningEffort != nil {
		req.ReasoningEffort = l.ReasoningEffort(req.Model)
	}
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
	limits := m.limits
	if limits.ForContext != nil {
		limits = limits.ForContext(ctx, req.Model)
	}
	callCtx := ctx
	if limits.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, limits.Timeout)
		defer cancel()
	}
	req = limits.apply(req)
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
		issue := responseIntegrityIssue(resp)
		if issue == "" {
			return resp, nil
		}
		if attempt == 1 {
			return Response{}, &callLimitError{reason: issue + " twice"}
		}
		if m.limits.OnResponseRetry != nil {
			m.limits.OnResponseRetry(ctx)
		}
		messages := make([]ChatMessage, len(req.Messages), len(req.Messages)+1)
		copy(messages, req.Messages)
		req.Messages = append(messages, ChatMessage{Role: RoleUser, Content: "Your previous generation was discarded: " + issue + ". No tool calls from it were executed. Return one small next action with complete JSON arguments, or a concise answer. Keep reasoning brief; do not draft the entire project in one response; split large files across steps."})
	}
	panic("unreachable")
}

// Providers may report tool_calls or stop even when the output cap cut off
// arguments. Validate the whole batch before exposing any call to the runtime.
func responseIntegrityIssue(resp Response) string {
	if resp.FinishReason == "length" {
		return "output truncated"
	}
	for _, call := range resp.ToolCalls {
		if !json.Valid([]byte(call.Arguments)) {
			return "incomplete or invalid tool arguments"
		}
	}
	return ""
}

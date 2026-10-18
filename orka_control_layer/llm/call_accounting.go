package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// CallAccountant is the policy seam at the provider boundary. Begin reserves
// BEFORE dispatch. The returned callback settles EXACTLY that attempt, including
// transport errors and partial streams, and must preserve reservations on error.
// It receives the unredacted response usage, never prompts or credentials in a
// persistent ledger. Install a run-scoped implementation using WithCallAccountant.
type CallAccountant interface {
	Begin(context.Context, Request) (func(context.Context, Response, error) error, error)
}

type callAccountantKey struct{}

func WithCallAccountant(ctx context.Context, a CallAccountant) context.Context {
	if a == nil {
		return ctx
	}
	return context.WithValue(ctx, callAccountantKey{}, a)
}
func CallAccountantFrom(ctx context.Context) CallAccountant {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(callAccountantKey{}).(CallAccountant)
	return a
}
func HasCallAccountant(ctx context.Context) bool { return CallAccountantFrom(ctx) != nil }

// AccountedClient sits INSIDE Retry and Limiter, so each actual exchange reserves
// once and time spent waiting for a limiter does not reserve a phantom call.
// Both Limiter(Retry(Metered(Accounted(provider)))) and
// Retry(Limiter(Metered(Accounted(provider)))) have this property.
type AccountedClient struct{ Client }

func NewAccounted(c Client) Client {
	if _, ok := c.(*AccountedClient); ok {
		return c
	}
	return &AccountedClient{Client: c}
}
func NewAccountedClient(c Client) Client { return NewAccounted(c) }

// AccountingError keeps the storage/quota cause observable while callLimitError
// prevents all existing retry/failover layers from spending another attempt.
type AccountingError struct {
	Operation string
	Cause     error
}

func (e *AccountingError) Error() string {
	return "usage accounting " + e.Operation + ": " + e.Cause.Error()
}
func (e *AccountingError) Unwrap() error { return e.Cause }
func accountingFailure(operation string, err error) error {
	return &callLimitError{reason: "usage accounting " + operation + " failed", cause: &AccountingError{Operation: operation, Cause: err}}
}

func (c *AccountedClient) invoke(ctx context.Context, req Request, call func(context.Context) (Response, error)) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	accountant := CallAccountantFrom(ctx)
	if accountant == nil {
		callCtx, owns := withLegacyAccountingOwner(ctx, c)
		resp, err := call(callCtx)
		if owns {
			ReportExternalUsage(callCtx, resp.Usage)
		}
		return resp, err
	}
	settle, err := accountant.Begin(ctx, req)
	if err != nil {
		return Response{}, accountingFailure("admission", err)
	}
	if settle == nil {
		return Response{}, accountingFailure("admission", errors.New("accountant returned no settlement"))
	}
	resp, callErr := call(ctx)
	// Cancellation ends execution, not accounting. A finite detached context also
	// protects accountants that do not themselves detach their database writes.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := settle(settleCtx, resp, callErr); err != nil {
		return resp, accountingFailure("settlement", errors.Join(callErr, err))
	}
	return resp, callErr
}
func (c *AccountedClient) Chat(ctx context.Context, req Request) (Response, error) {
	return c.invoke(ctx, req, func(callCtx context.Context) (Response, error) { return c.Client.Chat(callCtx, req) })
}
func (c *AccountedClient) ChatStream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	sc, ok := c.Client.(StreamingClient)
	if !ok {
		return c.Chat(ctx, req)
	}
	return c.invoke(ctx, req, func(callCtx context.Context) (Response, error) { return sc.ChatStream(callCtx, req, onDelta) })
}

// ExternalCallSpec reserves an entire bounded remote invocation whose internal
// attempts cannot synchronously contact this ledger. PromptTokens is a PER STEP
// estimate including page/image context; completion defaults to 4096 per step.
type ExternalCallSpec struct {
	CallID, Source                              string
	MaxSteps, PromptTokens, MaxCompletionTokens int
}
type externalCallSpecKey struct{}

func ExternalCallFrom(ctx context.Context) (ExternalCallSpec, bool) {
	spec, ok := ctx.Value(externalCallSpecKey{}).(ExternalCallSpec)
	return spec, ok
}

// BeginExternalCall installs a private collector for ReportExternalUsage. The
// finish callback's complete flag means ALL exchanges reported authoritative
// usage, including any known zero calls. Pass false on missing usage/transport
// interruption even when some counts were received. The collector never sends
// the same usage to the old parent sink, and repeated finish calls are safe.
// Legacy callers without a hook preserve the original sink behavior.
func BeginExternalCall(ctx context.Context, spec ExternalCallSpec) (context.Context, func(context.Context, bool, error) error, error) {
	if spec.CallID == "" || spec.Source == "" || spec.MaxSteps <= 0 || spec.PromptTokens < 0 || spec.MaxCompletionTokens < 0 {
		return ctx, nil, errors.New("invalid external usage reservation")
	}
	if spec.MaxCompletionTokens == 0 {
		spec.MaxCompletionTokens = 4096
	}
	if spec.PromptTokens > int(^uint(0)>>1)-spec.MaxCompletionTokens || spec.MaxSteps > int(^uint(0)>>1)/(spec.PromptTokens+spec.MaxCompletionTokens) {
		return ctx, nil, errors.New("external usage reservation overflows")
	}
	accountant := CallAccountantFrom(ctx)
	if accountant == nil {
		return ctx, func(_ context.Context, _ bool, err error) error { return err }, nil
	}
	callCtx := context.WithValue(ctx, externalCallSpecKey{}, spec)
	settle, err := accountant.Begin(callCtx, Request{MaxTokens: spec.MaxCompletionTokens})
	if err != nil {
		return ctx, nil, accountingFailure("external admission", err)
	}
	if settle == nil {
		return ctx, nil, accountingFailure("external admission", errors.New("accountant returned no settlement"))
	}
	collector := &externalUsageCollector{}
	callCtx = WithUsageSink(callCtx, collector)
	finish := func(finishCtx context.Context, complete bool, callErr error) error {
		collector.mu.Lock()
		defer collector.mu.Unlock()
		if collector.invalid != nil {
			return accountingFailure("external settlement", errors.Join(callErr, collector.invalid))
		}
		if collector.finished {
			return collector.result
		}
		resp := Response{Usage: Usage{Known: complete && !collector.incomplete, Incomplete: !complete || collector.incomplete, PromptTokens: collector.prompt, CompletionTokens: collector.completion, TotalTokens: collector.prompt + collector.completion}}
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(finishCtx), 10*time.Second)
		defer cancel()
		if err := settle(settleCtx, resp, callErr); err != nil {
			return accountingFailure("external settlement", errors.Join(callErr, err))
		}
		collector.finished = true
		collector.result = callErr
		return callErr
	}
	return callCtx, finish, nil
}

type externalUsageCollector struct {
	mu                 sync.Mutex
	prompt, completion int
	invalid            error
	incomplete         bool
	finished           bool
	result             error
}

func (c *externalUsageCollector) AddUsage(p, n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invalid != nil {
		return
	}
	if c.finished {
		c.invalid = errors.New("external usage received after settlement")
		return
	}
	maxInt := int(^uint(0) >> 1)
	if p < 0 || n < 0 || p > maxInt-n || c.prompt > maxInt-c.completion || p+n > maxInt-(c.prompt+c.completion) {
		c.invalid = fmt.Errorf("invalid external token counts")
		return
	}
	c.prompt += p
	c.completion += n
}

// Ownership is per exchange context, not a mutable property of a pooled client.
// It permits Metered(Accounted(raw)) and Accounted(Metered(raw)) to preserve one
// legacy report when no new accountant is installed.
type legacyAccountingOwnerKey struct{}

func withLegacyAccountingOwner(ctx context.Context, owner any) (context.Context, bool) {
	if ctx.Value(legacyAccountingOwnerKey{}) != nil {
		return ctx, false
	}
	return context.WithValue(ctx, legacyAccountingOwnerKey{}, owner), true
}

func (c *externalUsageCollector) report(u Usage) {
	if u.Incomplete {
		c.mu.Lock()
		c.incomplete = true
		c.mu.Unlock()
	}
	maxInt := int(^uint(0) >> 1)
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 || u.PromptTokens > maxInt-u.CompletionTokens {
		c.mu.Lock()
		c.invalid = errors.New("invalid external provider usage")
		c.mu.Unlock()
		return
	}
	completion := u.CompletionTokens
	if u.TotalTokens > u.PromptTokens+completion {
		completion = u.TotalTokens - u.PromptTokens
	}
	c.AddUsage(u.PromptTokens, completion)
}

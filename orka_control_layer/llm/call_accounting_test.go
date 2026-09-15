package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeCallAccountant struct {
	begins, settles     int
	beginErr, settleErr error
	responses           []Response
}

func (f *fakeCallAccountant) Begin(context.Context, Request) (func(context.Context, Response, error) error, error) {
	f.begins++
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return func(ctx context.Context, resp Response, err error) error {
		f.settles++
		f.responses = append(f.responses, resp)
		return f.settleErr
	}, nil
}

type accountingProvider struct {
	calls int
	run   func(context.Context, int) (Response, error)
}

func (p *accountingProvider) Chat(ctx context.Context, req Request) (Response, error) {
	p.calls++
	return p.run(ctx, p.calls)
}
func TestCallAccountingReservesEveryRetryAndDoesNotDoubleMeter(t *testing.T) {
	f := &fakeCallAccountant{}
	legacy := &accountingTestSink{}
	p := &accountingProvider{run: func(ctx context.Context, n int) (Response, error) {
		if f.begins != n {
			t.Fatal("dispatch without reservation")
		}
		if n == 1 {
			return Response{}, &APIError{Status: 503}
		}
		return Response{Usage: Usage{PromptTokens: 5, CompletionTokens: 2}}, nil
	}}
	c := NewRetry(NewLimiter(NewAccountedClient(NewMetered(p, nil)), 1, 0), RetryConfig{MaxAttempts: 2, BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond})
	ctx := WithCallAccountant(WithUsageSink(context.Background(), legacy), f)
	if _, err := c.Chat(ctx, Request{}); err != nil {
		t.Fatal(err)
	}
	if f.begins != 2 || f.settles != 2 || legacy.tokens != 0 {
		t.Fatalf("begin %d settle %d duplicate legacy %d", f.begins, f.settles, legacy.tokens)
	}
}

type accountingTestSink struct{ tokens int }

func (s *accountingTestSink) AddUsage(p, c int) { s.tokens += p + c }
func TestCallAccountingErrorsStopRetryBeforeAnotherPaidCall(t *testing.T) {
	for _, admission := range []bool{true, false} {
		f := &fakeCallAccountant{}
		if admission {
			f.beginErr = errors.New("quota denied")
		} else {
			f.settleErr = errors.New("ledger unavailable")
		}
		p := &accountingProvider{run: func(context.Context, int) (Response, error) { return Response{}, &APIError{Status: 503} }}
		c := NewRetry(NewAccountedClient(p), RetryConfig{MaxAttempts: 3, BaseDelay: time.Nanosecond})
		_, err := c.Chat(WithCallAccountant(context.Background(), f), Request{})
		want := 1
		if admission {
			want = 0
		}
		if err == nil || !IsCallLimit(err) || p.calls != want || f.begins != 1 {
			t.Fatalf("calls %d begin %d err %v", p.calls, f.begins, err)
		}
	}
}
func TestExternalCallAggregatesKnownAndIncompleteUsage(t *testing.T) {
	for _, complete := range []bool{true, false} {
		f := &fakeCallAccountant{}
		legacy := &accountingTestSink{}
		ctx := WithCallAccountant(WithUsageSink(context.Background(), legacy), f)
		callCtx, finish, err := BeginExternalCall(ctx, ExternalCallSpec{CallID: "gui", Source: "gui", MaxSteps: 2, PromptTokens: 100, MaxCompletionTokens: 4096})
		if err != nil {
			t.Fatal(err)
		}
		ReportExternalUsage(callCtx, Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 18})
		ReportExternalUsage(callCtx, Usage{PromptTokens: 2, CompletionTokens: 1})
		if err := finish(ctx, complete, nil); err != nil {
			t.Fatal(err)
		}
		if f.begins != 1 || f.settles != 1 || legacy.tokens != 0 {
			t.Fatalf("double metering: %+v %d", f, legacy.tokens)
		}
		u := f.responses[0].Usage
		if u.PromptTokens != 12 || u.CompletionTokens != 9 || u.TotalTokens != 21 || u.Known != complete {
			t.Fatalf("aggregate %+v", u)
		}
	}
}
func TestAccountedClientWithoutHookPreservesLegacyMeter(t *testing.T) {
	sink := &accountingTestSink{}
	p := &accountingProvider{run: func(context.Context, int) (Response, error) {
		return Response{Usage: Usage{PromptTokens: 3, CompletionTokens: 2}}, nil
	}}
	c := NewAccountedClient(NewMetered(p, nil))
	if _, err := c.Chat(WithUsageSink(context.Background(), sink), Request{}); err != nil {
		t.Fatal(err)
	}
	if sink.tokens != 5 {
		t.Fatal(sink.tokens)
	}
}

func TestAccountedLegacySinkOwnedOnceForEitherWrapperOrder(t *testing.T) {
	for _, order := range []string{"accounted", "outer-metered", "inner-metered"} {
		sink := &accountingTestSink{}
		p := &accountingProvider{run: func(context.Context, int) (Response, error) {
			return Response{Usage: Usage{PromptTokens: 3, CompletionTokens: 2}}, nil
		}}
		var c Client = NewAccounted(p)
		if order == "outer-metered" {
			c = NewMetered(c, nil)
		}
		if order == "inner-metered" {
			c = NewAccounted(NewMetered(p, nil))
		}
		if _, err := c.Chat(WithUsageSink(context.Background(), sink), Request{}); err != nil {
			t.Fatal(err)
		}
		if sink.tokens != 5 {
			t.Fatalf("%s charged %d", order, sink.tokens)
		}
	}
}

type accountingStreamProvider struct{ calls int }

func (p *accountingStreamProvider) Chat(context.Context, Request) (Response, error) {
	return Response{}, errors.New("wrong non-stream dispatch")
}
func (p *accountingStreamProvider) ChatStream(ctx context.Context, req Request, delta func(string)) (Response, error) {
	p.calls++
	delta("partial")
	return Response{Usage: Usage{Known: true, PromptTokens: 7, CompletionTokens: 3}}, errors.New("stream disconnected")
}
func TestCallAccountingSettlesPartialStreamWithoutReplaying(t *testing.T) {
	f := &fakeCallAccountant{}
	p := &accountingStreamProvider{}
	c := NewRetry(NewAccounted(p), RetryConfig{MaxAttempts: 3, BaseDelay: time.Nanosecond})
	ctx, cancel := context.WithCancel(WithCallAccountant(context.Background(), f))
	_, err := c.ChatStream(ctx, Request{}, func(string) { cancel() })
	if err == nil || p.calls != 1 || f.begins != 1 || f.settles != 1 || f.responses[0].Usage.TotalTokens != 0 || !f.responses[0].Usage.Known {
		t.Fatalf("partial accounting calls=%d %+v err=%v", p.calls, f, err)
	}
}

func TestExternalUsageInvalidCountsCannotBecomeKnownZero(t *testing.T) {
	f := &fakeCallAccountant{}
	callCtx, finish, err := BeginExternalCall(WithCallAccountant(context.Background(), f), ExternalCallSpec{CallID: "bad", Source: "gui", MaxSteps: 1, PromptTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	ReportExternalUsage(callCtx, Usage{PromptTokens: -1})
	if err := finish(context.Background(), true, nil); err == nil {
		t.Fatal("invalid external count silently normalized into free usage")
	}
	if f.settles != 0 {
		t.Fatal("invalid usage released reservation")
	}
}

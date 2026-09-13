package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// thinkThenDrop serves a stream that emits reasoning and then hangs up without
// [DONE] — a provider connection dying mid-think. It counts how many times it was
// called, which is the whole question.
func thinkThenDrop(calls *int32, reasoningOnly bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"let me think about the whole scene first\"}}]}\n\n")
		if !reasoningOnly {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"}}]}\n\n")
		}
		fl.Flush()
		// Hijack and close without finishing the chunked body: the client sees an
		// unexpected EOF, exactly like a dropped provider connection.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
}

// The tripling this fixes: a stream that produced only REASONING before dying
// was treated as never started and sent again from the top, so the model
// re-thought everything on each attempt.
func TestAStreamThatOnlyReasonedIsNotReplayed(t *testing.T) {
	var calls int32
	srv := thinkThenDrop(&calls, true)
	defer srv.Close()

	r := NewRetry(NewOpenAIClient(srv.URL, "k"), RetryConfig{MaxAttempts: 3})
	_, err := r.ChatStream(context.Background(), Request{Model: "m"}, func(string) {})
	if err == nil {
		t.Fatal("a dropped stream reported success")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("the provider was called %d times; reasoning had already streamed, so a retry re-thinks from scratch", n)
	}
}

// The UI's own reasoning sink must still receive the deltas — the retry's
// wrapper observes them, it does not swallow them.
func TestTheReasoningSinkStillSeesTheDeltas(t *testing.T) {
	var calls int32
	srv := thinkThenDrop(&calls, true)
	defer srv.Close()

	var got strings.Builder
	ctx := WithReasoningSink(context.Background(), func(d string) { got.WriteString(d) })
	r := NewRetry(NewOpenAIClient(srv.URL, "k"), RetryConfig{MaxAttempts: 3})
	_, _ = r.ChatStream(ctx, Request{Model: "m"}, func(string) {})
	if !strings.Contains(got.String(), "think about the whole scene") {
		t.Fatalf("reasoning sink received %q", got.String())
	}
}

// A failure before ANY output is still worth retrying — that is what retry is
// for, and the fix must not have removed it.
func TestAStreamThatNeverProducedAnythingIsStillRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer srv.Close()

	r := NewRetry(NewOpenAIClient(srv.URL, "k"), RetryConfig{MaxAttempts: 3, BaseDelay: 1})
	_, _ = r.ChatStream(context.Background(), Request{Model: "m"}, func(string) {})
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("called %d times; a stream that produced nothing should get all 3 attempts", n)
	}
}

// What a dropped stream had produced must survive the error, so it can be
// reported. The 811-second call that prompted this left no record at all.
func TestADroppedStreamReturnsWhatArrived(t *testing.T) {
	var calls int32
	srv := thinkThenDrop(&calls, false)
	defer srv.Close()

	resp, err := NewOpenAIClient(srv.URL, "k").ChatStream(context.Background(), Request{Model: "m"}, func(string) {})
	if err == nil {
		t.Fatal("expected a stream error")
	}
	if !strings.Contains(resp.Reasoning, "whole scene") || resp.Content != "partial answer" {
		t.Fatalf("partial output was discarded: reasoning=%q content=%q", resp.Reasoning, resp.Content)
	}
}

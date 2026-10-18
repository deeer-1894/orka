package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
)

func TestBudgetProviderUsageCompletenessThroughTransport(t *testing.T) {
	cases := []struct {
		name, usage string
		known       bool
		tokens      int
	}{
		{"missing", "", false, 0}, {"null", "null", false, 0}, {"empty", `{}`, false, 0},
		{"prompt-only-zero", `{"prompt_tokens":0}`, false, 0},
		{"partial-positive", `{"prompt_tokens":100}`, false, 100},
		{"completion-only", `{"completion_tokens":5}`, false, 5},
		{"complete-zero", `{"prompt_tokens":0,"completion_tokens":0}`, true, 0},
		{"complete", `{"prompt_tokens":10,"completion_tokens":2}`, true, 12},
		{"total-zero", `{"total_tokens":0}`, true, 0},
		{"total-only", `{"total_tokens":15}`, true, 15},
		{"total-partial", `{"prompt_tokens":10,"total_tokens":15}`, true, 15},
		{"null-counter", `{"prompt_tokens":null,"completion_tokens":2}`, false, 2},
		{"string-counter", `{"prompt_tokens":10,"completion_tokens":"2"}`, false, 10},
		{"negative-counter", `{"prompt_tokens":10,"completion_tokens":-2}`, false, 10},
		{"fraction-counter", `{"prompt_tokens":10,"completion_tokens":2.5}`, false, 10},
		{"overflow-counter", `{"prompt_tokens":10,"completion_tokens":99999999999999999999999999}`, false, 10},
		{"contradictory-total", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":5}`, false, 12},
		{"invalid-object", `[]`, false, 0},
	}
	for _, mode := range []string{"chat", "stream", "http-error", "stream-http-error"} {
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ledger := &fakeLedger{}
				session := budgetSession(t, ledger, "fixture", "root", 100000, 100000)
				reserved := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "" {
						t.Error("unexpected credentials")
					}
					reserved = session.Snapshot().ReservedTokens
					if reserved <= 0 {
						t.Error("wire call without reservation")
					}
					usage := ""
					if tc.usage != "" {
						usage = `,"usage":` + tc.usage
					}
					body := `{"choices":[{"message":{"content":"ok"},"delta":{"content":"ok"},"finish_reason":"stop"}]` + usage + `}`
					if mode == "http-error" || mode == "stream-http-error" {
						w.WriteHeader(503)
					}
					if mode == "stream" {
						fmt.Fprintln(w, "data: "+body+"\n\ndata: [DONE]\n")
					} else {
						fmt.Fprint(w, body)
					}
				}))
				defer server.Close()
				raw := llm.NewOpenAIClient(server.URL, "")
				client := llm.NewLimiter(llm.NewRetry(llm.NewMetered(llm.NewAccounted(raw), nil), llm.RetryConfig{MaxAttempts: 1}), 1, 0)
				req := llm.Request{Model: "fake", MaxTokens: 100}
				ctx := session.Context(context.Background())
				var response llm.Response
				if mode == "stream" || mode == "stream-http-error" {
					response, _ = client.ChatStream(ctx, req, func(string) {})
				} else {
					response, _ = client.Chat(ctx, req)
				}
				// Retry.Chat returns an empty response on its final error; accounting
				// runs inside Retry and must still preserve the provider settlement.
				if mode != "http-error" && response.Usage.Known != tc.known {
					t.Errorf("wire known=%v, want %v: %+v", response.Usage.Known, tc.known, response.Usage)
				}
				want := tc.tokens
				if !tc.known {
					want = max(reserved, want)
				}
				got := session.Snapshot()
				if got.UsedTokens != want || got.ReservedTokens != 0 || (got.UnknownCalls == 0) != tc.known {
					t.Fatalf("usage classification released/duplicated allowance: %+v want tokens=%d known=%v", got, want, tc.known)
				}
				daily, err := session.DailySnapshot(ctx)
				if err != nil || daily.UsedTokens != want || daily.ReservedTokens != 0 {
					t.Fatalf("daily ledger disagrees: %+v %v", daily, err)
				}
			})
		}
	}
}

func TestBudgetExplicitIncompleteCannotUseLegacyFallback(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		for _, known := range []bool{false, true} {
			session := budgetSession(t, &fakeLedger{}, "fixture", "root", 10000, 10000)
			client := llm.NewAccounted(llm.NewMock(llm.Response{Usage: llm.Usage{Known: known, Incomplete: incomplete, PromptTokens: 10}}))
			if _, err := client.Chat(session.Context(context.Background()), llm.Request{MaxTokens: 100}); err != nil {
				t.Fatal(err)
			}
			got := session.Snapshot()
			if incomplete && (got.UnknownCalls != 1 || got.UsedTokens <= 10) {
				t.Fatalf("explicit incomplete laundered: %+v", got)
			}
			if !incomplete && (got.UnknownCalls != 0 || got.UsedTokens != 10) {
				t.Fatalf("legacy positive compatibility lost: %+v", got)
			}
		}
	}
}

func TestBudgetExternalIncompleteOverridesTerminalComplete(t *testing.T) {
	session := budgetSession(t, &fakeLedger{}, "fixture", "root", 10000, 10000)
	ctx, finish, err := llm.BeginExternalCall(session.Context(context.Background()), llm.ExternalCallSpec{CallID: "gui", Source: "gui", MaxSteps: 1, PromptTokens: 100, MaxCompletionTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	llm.ReportExternalUsage(ctx, llm.Usage{Incomplete: true, PromptTokens: 10})
	if err := finish(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	got := session.Snapshot()
	if got.UnknownTokens != 200 || got.UnknownCalls != 1 || got.ReservedTokens != 0 {
		t.Fatalf("GUI incomplete released allowance: %+v", got)
	}
}

func TestBudgetMalformedTransportRetainsObservedUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		session := budgetSession(t, &fakeLedger{}, "fixture", "root", 10000, 10000)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := `{"choices":false,"usage":{"prompt_tokens":500,"completion_tokens":0}}`
			if stream {
				fmt.Fprint(w, "data: "+body+"\n\n")
			} else {
				fmt.Fprint(w, body)
			}
		}))
		client := llm.NewAccounted(llm.NewOpenAIClient(server.URL, ""))
		ctx := session.Context(context.Background())
		var err error
		if stream {
			_, err = client.(llm.StreamingClient).ChatStream(ctx, llm.Request{MaxTokens: 100}, func(string) {})
		} else {
			_, err = client.Chat(ctx, llm.Request{MaxTokens: 100})
		}
		server.Close()
		if err == nil {
			t.Fatal("malformed response succeeded")
		}
		got := session.Snapshot()
		if got.UnknownCalls != 1 || got.UnknownTokens != 500 || got.ReservedTokens != 0 {
			t.Fatalf("partial decode released/erased usage: %+v", got)
		}
	}
}

package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderUsageDistinguishesMissingAndKnownZero(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		known      bool
		status     int
	}{
		{"missing", `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, false, 200},
		{"empty", `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`, false, 200},
		{"zero", `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0}}`, true, 200},
		{"error", `{"error":{"message":"failed"},"usage":{"prompt_tokens":10,"completion_tokens":2}}`, true, 200},
		{"http error", `{"error":{"message":"failed"},"usage":{"prompt_tokens":10,"completion_tokens":2}}`, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer ts.Close()
			resp, _ := NewOpenAIClient(ts.URL, "").Chat(context.Background(), Request{Model: "m"})
			if resp.Usage.Known != tc.known {
				t.Fatal("usage provenance lost", resp.Usage)
			}
		})
	}
}
func TestStreamErrorRetainsAlreadyReadUsage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2},\"error\":{\"message\":\"failed\"}}\n\n")
	}))
	defer ts.Close()
	resp, err := NewOpenAIClient(ts.URL, "").ChatStream(context.Background(), Request{Model: "m"}, func(string) {})
	if err == nil || !resp.Usage.Known || resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 2 {
		t.Fatal("error stream discarded usage", resp.Usage, err)
	}
}

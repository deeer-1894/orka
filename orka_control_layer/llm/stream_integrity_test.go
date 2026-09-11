package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestIncompleteStreamNeverReachesAgent(t *testing.T) {
	prefix := `data: {"choices":[{"delta":{"reasoning_content":"partial reasoning","tool_calls":[{"index":0,"id":"unsafe","function":{"name":"write","arguments":"{}"}}]}}]}` + "\n\n"
	for name, suffix := range map[string]string{
		"eof": "", "done_without_finish": "data: [DONE]\n\n",
		"corrupt_frame_then_finish": "data: {broken json}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, prefix+suffix)
			}))
			defer server.Close()
			m := NewEinoModel(NewOpenAIClient(server.URL, ""), "test").WithCallLimits(CallLimits{MaxTokens: 32, Timeout: time.Second})
			stream, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("write")})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			msg, err := stream.Recv()
			if err == nil || msg != nil {
				t.Fatalf("incomplete response accepted: msg=%+v err=%v", msg, err)
			}
		})
	}
}

func TestIncompleteNonStreamNeverReachesAgent(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		short      bool
	}{
		{"missing_finish", `{"choices":[{"message":{"role":"assistant","reasoning_content":"partial","tool_calls":[{"id":"c1","function":{"name":"write","arguments":"{}"}}]}}]}`, false},
		{"short_body", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.short {
					w.Header().Set("Content-Length", fmt.Sprint(len(tc.body)+10))
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			m := NewEinoModel(NewOpenAIClient(server.URL, ""), "test").WithCallLimits(CallLimits{MaxTokens: 32})
			msg, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("task")})
			if err == nil || msg != nil {
				t.Fatalf("incomplete nonstream response accepted: %+v, %v", msg, err)
			}
		})
	}
}

func TestRejectedNonStreamRetainsReportedUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"unsafe","reasoning_content":"partial","tool_calls":[{"id":"c1","function":{"name":"write","arguments":"{}"}}]}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	}))
	defer server.Close()
	sink := &limitUsage{}
	ctx := WithUsageSink(context.Background(), sink)
	response, err := NewMetered(NewOpenAIClient(server.URL, ""), nil).Chat(ctx, Request{Model: "test"})
	if err == nil || response.Content != "" || response.Reasoning != "" || len(response.ToolCalls) != 0 {
		t.Fatalf("rejected response leaked: %+v, %v", response, err)
	}
	if sink.input != 11 || sink.output != 7 || response.Usage.TotalTokens != 18 {
		t.Fatalf("reported usage lost: sink=%+v response=%+v", sink, response)
	}
}

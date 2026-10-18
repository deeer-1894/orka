package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestAcceptedReasoningSurvivesToolRoundTrip(t *testing.T) {
	previous := fromResponse(Response{Reasoning: "Compare the remaining inputs, then use the result.", ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: `{}`}}, FinishReason: "tool_calls"})
	mock := NewMock(Response{Content: "done"})
	m := NewEinoModel(mock, "deepseek-v4-pro")
	_, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("task"), previous, schema.ToolMessage("found", "call-1")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(toWireRequest(mock.Requests[0]))
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []map[string]any `json:"messages"`
	}
	if err = json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if req.Messages[1]["reasoning_content"] != previous.ReasoningContent {
		t.Fatalf("accepted reasoning lost from next tool round: %s", b)
	}
	for _, i := range []int{0, 2} {
		if _, ok := req.Messages[i]["reasoning_content"]; ok {
			t.Fatalf("reasoning leaked to role %v", req.Messages[i]["role"])
		}
	}
}

func TestReasoningOptionsReachBothHTTPModes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			captured := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				captured <- body
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			client := NewOpenAIClient(server.URL, "test")
			request := Request{Model: "known", ReasoningEffort: "low", Messages: []ChatMessage{{Role: RoleUser, Content: "task", Reasoning: "must not leak"}, {Role: RoleAssistant, Reasoning: "retained", ToolCalls: []ToolCall{{ID: "c1", Name: "lookup", Arguments: `{}`}}}, {Role: RoleTool, ToolCallID: "c1", Content: "result", Reasoning: "must not leak"}}}
			var err error
			if stream {
				_, err = client.ChatStream(context.Background(), request, func(string) {})
			} else {
				_, err = client.Chat(context.Background(), request)
			}
			if err != nil {
				t.Fatal(err)
			}
			got := <-captured
			if got["reasoning_effort"] != "low" {
				t.Fatal("effort omitted", got)
			}
			msgs := got["messages"].([]any)
			if msgs[1].(map[string]any)["reasoning_content"] != "retained" {
				t.Fatal("reasoning lost", got)
			}
			for _, i := range []int{0, 2} {
				if _, ok := msgs[i].(map[string]any)["reasoning_content"]; ok {
					t.Fatal("non-assistant reasoning leaked")
				}
			}
		})
	}
}

func TestEmptyReasoningOptionsAreOmitted(t *testing.T) {
	b, err := json.Marshal(toWireRequest(Request{Model: "generic", Messages: []ChatMessage{{Role: RoleAssistant, Content: "ok"}}}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatal("unspecified effort emitted")
	}
	if _, ok := got["messages"].([]any)[0].(map[string]any)["reasoning_content"]; ok {
		t.Fatal("empty reasoning emitted")
	}
}

func TestEffortPolicyAppliesToStreamRetriesWithoutStateLeak(t *testing.T) {
	mock := NewMock(Response{Reasoning: "discarded", FinishReason: "length"}, Response{Content: "ok", FinishReason: "stop"}, Response{Content: "ok", FinishReason: "stop"})
	base := NewEinoModel(limitStreamClient{mock}, "known")
	m := base.WithCallLimits(CallLimits{MaxTokens: 32, ReasoningEffort: func(model string) string {
		if model == "known" {
			return "low"
		}
		return ""
	}})
	sr, err := m.Stream(context.Background(), []*schema.Message{schema.UserMessage("task")})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err = sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	sr.Close()
	for _, req := range mock.Requests {
		if req.ReasoningEffort != "low" {
			t.Fatal("retry lost effort")
		}
		for _, m := range req.Messages {
			if m.Reasoning == "discarded" {
				t.Fatal("truncated reasoning leaked")
			}
		}
	}
	_, err = base.Generate(context.Background(), []*schema.Message{schema.UserMessage("next")})
	if err != nil {
		t.Fatal(err)
	}
	if mock.Requests[2].ReasoningEffort != "" {
		t.Fatal("policy leaked to base model")
	}
}

func TestHTTPAcceptedReasoningContinuesAcrossEinoToolRounds(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			captured := make(chan map[string]any, 2)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				captured <- req
				calls++
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					if calls == 1 {
						fmt.Fprint(w, `data: {"choices":[{"delta":{"reasoning_content":"accepted chain","tool_calls":[{"index":0,"id":"c1","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
					}
				} else if calls == 1 {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","reasoning_content":"accepted chain","tool_calls":[{"id":"c1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			m := NewEinoModel(NewOpenAIClient(server.URL, ""), "test").WithCallLimits(CallLimits{MaxTokens: 64})
			generate := func(in []*schema.Message) *schema.Message {
				if !streaming {
					msg, err := m.Generate(context.Background(), in)
					if err != nil {
						t.Fatal(err)
					}
					return msg
				}
				sr, err := m.Stream(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				defer sr.Close()
				msg, err := sr.Recv()
				if err != nil {
					t.Fatal(err)
				}
				return msg
			}
			input := []*schema.Message{schema.UserMessage("task")}
			first := generate(input)
			if first.ReasoningContent != "accepted chain" || len(first.ToolCalls) != 1 {
				t.Fatal("first response lost accepted data", first)
			}
			input = append(input, first, schema.ToolMessage("result", first.ToolCalls[0].ID))
			second := generate(input)
			if second.Content != "done" {
				t.Fatal(second)
			}
			<-captured
			request := <-captured
			messages := request["messages"].([]any)
			if messages[1].(map[string]any)["reasoning_content"] != "accepted chain" {
				t.Fatal("reasoning lost in real next HTTP", request)
			}
			if messages[2].(map[string]any)["tool_call_id"] != "c1" {
				t.Fatal("tool result lost link")
			}
		})
	}
}

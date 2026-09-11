package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type limitStreamClient struct{ *MockClient }

func (c limitStreamClient) ChatStream(ctx context.Context, req Request, emit func(string)) (Response, error) {
	r, e := c.Chat(ctx, req)
	emit(r.Content)
	return r, e
}

func TestCallLimitsDiscardTruncatedStream(t *testing.T) {
	mock := NewMock(Response{Content: "discard me", FinishReason: "length", ToolCalls: []ToolCall{{ID: "bad", Name: "write", Arguments: `{"path":`}}}, Response{Content: "accepted", FinishReason: "tool_calls", ToolCalls: []ToolCall{{ID: "good", Name: "write", Arguments: `{"path":"a"}`}}})
	m := NewEinoModel(limitStreamClient{mock}, "m").WithCallLimits(CallLimits{FirstMaxTokens: 32, MaxTokens: 64, Timeout: time.Second})
	in := []*schema.Message{schema.UserMessage("task")}
	sr, e := m.Stream(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	defer sr.Close()
	var content string
	var calls []schema.ToolCall
	for {
		msg, e := sr.Recv()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		content += msg.Content
		calls = append(calls, msg.ToolCalls...)
	}
	if content != "accepted" || len(calls) != 1 || calls[0].ID != "good" {
		t.Fatalf("rejected output leaked: %q %+v", content, calls)
	}
	if mock.Calls() != 2 {
		t.Fatal(mock.Calls())
	}
	for _, r := range mock.Requests {
		if r.MaxTokens != 32 {
			t.Fatal(r.MaxTokens)
		}
		for _, msg := range r.Messages {
			if strings.Contains(msg.Content, "discard me") || len(msg.ToolCalls) > 0 {
				t.Fatal("truncated response entered retry history")
			}
		}
	}
	if len(in) != 1 || len(mock.Requests[1].Messages) != 2 {
		t.Fatal("retry mutated original input")
	}
}

func TestCallLimitsCapOptionsWithoutLeakingState(t *testing.T) {
	mock := NewMock()
	base := NewEinoModel(mock, "m")
	limited := base.WithCallLimits(CallLimits{FirstMaxTokens: 32, MaxTokens: 64, Timeout: time.Second}).ForAgent("test")
	bound, e := limited.WithTools(nil)
	if e != nil {
		t.Fatal(e)
	}
	first := []*schema.Message{schema.UserMessage("hi")}
	later := []*schema.Message{schema.UserMessage("hi"), schema.AssistantMessage("working", nil), schema.UserMessage("continue")}
	for _, tc := range []struct {
		m    model.ToolCallingChatModel
		in   []*schema.Message
		opts []model.Option
		want int
	}{{bound, first, nil, 32}, {bound, later, nil, 64}, {bound, later, []model.Option{model.WithMaxTokens(8)}, 8}, {bound, first, []model.Option{model.WithMaxTokens(999)}, 32}, {base, first, nil, 0}, {bound, first, nil, 32}} {
		if _, e := tc.m.Generate(context.Background(), tc.in, tc.opts...); e != nil {
			t.Fatal(e)
		}
		if got := mock.Requests[len(mock.Requests)-1].MaxTokens; got != tc.want {
			t.Fatalf("cap=%d want %d", got, tc.want)
		}
	}
}

type blockingLimitClient struct {
	calls     int
	deadlines []time.Time
}

func (c *blockingLimitClient) Chat(ctx context.Context, r Request) (Response, error) {
	c.calls++
	d, _ := ctx.Deadline()
	c.deadlines = append(c.deadlines, d)
	if c.calls == 1 {
		return Response{FinishReason: "length"}, nil
	}
	<-ctx.Done()
	return Response{}, ctx.Err()
}
func TestCallLimitDeadlineSharedWithLengthRetry(t *testing.T) {
	c := &blockingLimitClient{}
	m := NewEinoModel(c, "m").WithCallLimits(CallLimits{MaxTokens: 32, Timeout: 20 * time.Millisecond})
	start := time.Now()
	_, e := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if !IsCallLimit(e) || !errors.Is(e, context.DeadlineExceeded) || IsTransient(context.Background(), e) {
		t.Fatalf("deadline error classified incorrectly: %v", e)
	}
	if c.calls != 2 || !c.deadlines[0].Equal(c.deadlines[1]) || time.Since(start) > time.Second {
		t.Fatalf("deadline was extended: %+v", c)
	}
}

type limitUsage struct{ input, output int }

func (s *limitUsage) AddUsage(in, out int) { s.input += in; s.output += out }
func TestCallLimitsMeterDiscardedAttempt(t *testing.T) {
	mock := NewMock(Response{FinishReason: "length", Usage: Usage{PromptTokens: 10, CompletionTokens: 32}}, Response{Content: "ok", FinishReason: "stop", Usage: Usage{PromptTokens: 15, CompletionTokens: 8}})
	sink := &limitUsage{}
	m := NewEinoModel(NewMetered(limitStreamClient{mock}, nil), "m").WithCallLimits(CallLimits{MaxTokens: 32, Timeout: time.Second})
	sr, e := m.Stream(WithUsageSink(context.Background(), sink), []*schema.Message{schema.UserMessage("hi")})
	if e != nil {
		t.Fatal(e)
	}
	defer sr.Close()
	for {
		_, e := sr.Recv()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if sink.input != 25 || sink.output != 40 {
		t.Fatalf("discarded usage lost: %+v", sink)
	}
}

func TestCallLimitsRespectParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := NewEinoModel(NewMock(Response{FinishReason: "length"}), "m").WithCallLimits(CallLimits{MaxTokens: 32, Timeout: time.Second})
	_, e := m.Generate(ctx, nil)
	if !errors.Is(e, context.Canceled) || IsCallLimit(e) {
		t.Fatalf("parent cancellation was reclassified: %v", e)
	}
}

package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

type steeringModel struct {
	calls            atomic.Int32
	entered, proceed chan struct{}
	tool             bool
	seen             chan llm.Request
}

func (m *steeringModel) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	n := m.calls.Add(1)
	if n == 1 {
		if m.tool {
			return llm.Response{ToolCalls: []llm.ToolCall{{ID: "once", Name: "echo", Arguments: `{"text":"original"}`}}, FinishReason: "tool_calls"}, nil
		}
		close(m.entered)
		select {
		case <-m.proceed:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
		return llm.Response{Content: "original answer", FinishReason: "stop"}, nil
	}
	m.seen <- req
	return llm.Response{Content: "updated answer", FinishReason: "stop"}, nil
}

type steeringTool struct {
	echoTool
	entered, proceed chan struct{}
	calls            atomic.Int32
}

func (t *steeringTool) Invoke(ctx context.Context, _ map[string]any) (string, error) {
	t.calls.Add(1)
	close(t.entered)
	select {
	case <-t.proceed:
		return "completed exactly once", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type steeringStore struct {
	adk.CheckPointStore
	writes, reads atomic.Int32
}

func (s *steeringStore) Set(ctx context.Context, id string, b []byte) error {
	s.writes.Add(1)
	return s.CheckPointStore.Set(ctx, id, b)
}
func (s *steeringStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	s.reads.Add(1)
	return s.CheckPointStore.Get(ctx, id)
}

func TestSteeringDuringExecution(t *testing.T) {
	for _, tool := range []bool{true, false} {
		name := "final-model-call"
		if tool {
			name = "in-flight-tool"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m := &steeringModel{entered: make(chan struct{}), proceed: make(chan struct{}), seen: make(chan llm.Request, 4), tool: tool}
			svc, _ := testService(t, m)
			svc.Cfg.Storage.BaseStoragePath = t.TempDir()
			ctx, release, err := svc.AdmitExecution(ctx, "owner", "conversation", "")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			store := &steeringStore{CheckPointStore: newCheckpointStore(svc.Cfg.Storage.BaseStoragePath)}
			ctx = withCheckpointStore(ctx, store)
			ctx = withAcceptance(ctx, svc.Cfg.Storage.BaseStoragePath, "owner", "conversation", ExecutionID(ctx))
			ctx = withJournal(ctx, newRunJournal(svc.Cfg.Storage.BaseStoragePath, ExecutionID(ctx), nil))
			if err := persistAcceptanceContract(ctx, []string{"original task"}); err != nil {
				t.Fatal(err)
			}
			col := &collector{}
			rc := &agent.RunContext{Ctx: ctx, Meta: messages.Meta{ConversationID: "conversation", RunID: ExecutionID(ctx), UserEmail: "owner"}, Vars: map[string]any{}, Send: col.sink}
			rc.Messages = []messages.Message{humanChat("original task", rc.Meta)}
			gate := &steeringTool{entered: make(chan struct{}), proceed: make(chan struct{})}
			var tools []agent.BaseTool
			entered, proceed := m.entered, m.proceed
			if tool {
				tools = []agent.BaseTool{gate}
				entered, proceed = gate.entered, gate.proceed
			}
			done := make(chan error, 1)
			go func() { done <- svc.runEino(ctx, rc, PipelineDeps{}, tools, m, "m", col.sink) }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("ended early: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			req := SteeringRequest{ConversationID: "conversation", RunID: ExecutionID(ctx), RequestID: "request-1", Message: "Change the result to BLUE"}
			if _, err := svc.Steer(ctx, "stranger", req); !errors.Is(err, ErrSteeringUnavailable) {
				t.Fatalf("owner check: %v", err)
			}
			receipt, err := svc.Steer(ctx, "owner", req)
			if err != nil {
				t.Fatal(err)
			}
			again, err := svc.Steer(ctx, "owner", req)
			if err != nil || again.ID != receipt.ID {
				t.Fatalf("duplicate: %v", err)
			}
			close(proceed)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case r := <-m.seen:
				var contents []string
				for _, x := range r.Messages {
					contents = append(contents, x.Content)
				}
				all := strings.Join(contents, "\n")
				if !strings.Contains(all, req.Message) || !strings.Contains(all, "original task") {
					t.Fatalf("instructions not received: %s", all)
				}
			default:
				t.Fatal("model never continued")
			}
			if tool && (gate.calls.Load() != 1 || store.writes.Load() == 0 || store.reads.Load() == 0) {
				t.Fatalf("tool=%d checkpoint writes=%d reads=%d", gate.calls.Load(), store.writes.Load(), store.reads.Load())
			}
			if m.calls.Load() != 2 {
				t.Fatalf("unexpected model calls %d", m.calls.Load())
			}
			req.RequestID = "too-late"
			if _, err := svc.Steer(ctx, "owner", req); !errors.Is(err, ErrSteeringUnavailable) {
				t.Fatalf("accepted after finish: %v", err)
			}
			history, err := svc.AcceptanceFor("owner", "conversation", ExecutionID(ctx))
			if err != nil || len(history.Contract.Requests) != 2 {
				t.Fatalf("contract: %+v %v", history, err)
			}
		})
	}
}

func TestSteeringPublicRunKeepsExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := &steeringModel{entered: make(chan struct{}), proceed: make(chan struct{}), seen: make(chan llm.Request, 4)}
	svc, _ := testService(t, m)
	svc.Cfg.Storage.BaseStoragePath = t.TempDir()
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) { return nil, nil, nil }
	ctx, release, err := svc.AdmitExecution(ctx, "owner", "conversation", "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	col := &collector{}
	done := make(chan string, 1)
	go func() {
		done <- svc.Run(ctx, ChatRunRequest{ConversationID: "conversation", UserEmail: "owner", Message: "original task"}, col.sink)
	}()
	select {
	case <-m.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = svc.Steer(ctx, "owner", SteeringRequest{ConversationID: "conversation", RunID: ExecutionID(ctx), RequestID: "change", Message: "BLUE"})
	if err != nil {
		t.Fatal(err)
	}
	close(m.proceed)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	starts, terminals := 0, 0
	for _, event := range col.byType(messages.EventTask) {
		if event.Meta.RunID != ExecutionID(ctx) {
			t.Fatalf("execution changed: %+v", event.Meta)
		}
		if event.Action == "start" {
			starts++
		}
		if event.Action == "done" || event.Action == "failed" || event.Action == "stopped" {
			terminals++
		}
	}
	if starts != 1 || terminals != 1 {
		t.Fatalf("starts %d terminals %d", starts, terminals)
	}
	select {
	case r := <-m.seen:
		found := false
		for _, x := range r.Messages {
			found = found || strings.Contains(x.Content, "BLUE")
		}
		if !found {
			t.Fatal("lost updated instruction")
		}
	default:
		t.Fatal("model never continued")
	}
}

func TestSteeringCloseAndAcceptAreAtomic(t *testing.T) {
	for n := 0; n < 100; n++ {
		i := newSteeringInbox()
		i.accept = func(r SteeringRequest) (messages.Message, error) { return humanChat(r.Message, messages.Meta{}), nil }
		start := make(chan struct{})
		ended := make(chan bool, 1)
		accepted := make(chan error, 1)
		go func() { <-start; ended <- i.finish() }()
		go func() { <-start; _, err := i.push(SteeringRequest{RequestID: "1", Message: "update"}); accepted <- err }()
		close(start)
		closed, err := <-ended, <-accepted
		if closed && err == nil || !closed && err != nil {
			t.Fatalf("close=%v acceptance=%v", closed, err)
		}
	}
}

func TestSteeringRestoresUnappliedAcceptedMessage(t *testing.T) {
	svc, _ := testService(t, llm.NewMock())
	base := t.TempDir()
	svc.Cfg.Storage.BaseStoragePath = base
	old := withAcceptance(context.Background(), base, "owner", "conversation", "previous")
	m := humanChat("new required constraint", messages.Meta{ConversationID: "conversation", UserEmail: "owner", RunID: "previous"})
	if err := persistSteeringRequest(old, m); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := svc.AdmitExecution(context.Background(), "owner", "conversation", "")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx = withRunResume(ctx, &runResume{Checkpoint: &runCheckpoint{AcceptanceRunIDs: []string{"previous"}}})
	ctx = withAcceptance(ctx, base, "owner", "conversation", ExecutionID(ctx))
	rc := &agent.RunContext{Ctx: ctx, Meta: messages.Meta{ConversationID: "conversation", UserEmail: "owner", RunID: ExecutionID(ctx)}}
	h := svc.prepareSteering(ctx, rc)
	state := &adk.ChatModelAgentState{}
	_, state, err = h.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Messages) != 1 || state.Messages[0].Content != m.Content {
		t.Fatalf("missing pending input: %+v", state.Messages)
	}
	// Rebuilding the middleware, as on another checkpoint resume, is idempotent.
	h = svc.prepareSteering(ctx, rc)
	_, state, err = h.BeforeModelRewriteState(ctx, state, nil)
	if err != nil || len(state.Messages) != 1 {
		t.Fatalf("duplicate input: %v count=%d", err, len(state.Messages))
	}
}

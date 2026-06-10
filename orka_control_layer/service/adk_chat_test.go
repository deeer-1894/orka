package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/checkpoint"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/message_utils"
	"github.com/orka-oss/orka_control_layer/obs"
)

type collector struct {
	mu   sync.Mutex
	msgs []messages.Message
}

func (c *collector) sink(m messages.Message) {
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
}

func (c *collector) byType(t messages.EventType) []messages.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []messages.Message
	for _, m := range c.msgs {
		if m.Type == t {
			out = append(out, m)
		}
	}
	return out
}

func (c *collector) clarifyKey() string {
	for _, m := range c.byType(messages.EventClarify) {
		if cm, ok := m.Payload.(messages.ClarifyMessage); ok {
			return cm.ResumeKey
		}
	}
	return ""
}

func testService(t *testing.T, mainLLM llm.Client) (*ChatService, checkpoint.Store) {
	t.Helper()
	cfg := &config.Config{}
	cfg.LLM.Model = "m"
	cfg.Agent.CheckpointTTLSec = 3600
	cpStore := checkpoint.NewMemoryStore()
	msg := message_utils.New(nil, 1.0, nil) // no Mongo
	return NewChatService(cfg, mainLLM, mainLLM, cpStore, msg, obs.NewMetrics(), nil), cpStore
}

func TestChat_NormalRun(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(llm.Response{Content: "hello there"}))
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "hi", ConversationID: "c1"}, col.sink)

	if chats := col.byType(messages.EventChat); len(chats) == 0 || chats[len(chats)-1].Content != "hello there" {
		t.Fatalf("assistant chat missing: %+v", chats)
	}
	done := col.byType(messages.EventTask)
	if len(done) == 0 || done[len(done)-1].Action != "done" {
		t.Fatalf("task done missing: %+v", done)
	}
}

func TestChat_ClarifyResumeAndDuplicateRejected(t *testing.T) {
	mock := llm.NewMock(
		llm.Response{ToolCalls: []llm.ToolCall{{ID: "1", Name: "clarify", Arguments: `{"question":"which?","options":["A","B"]}`}}},
		llm.Response{Content: "resolved with A"},
	)
	svc, _ := testService(t, mock)

	// 1) run -> clarify + checkpoint saved
	col := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "ambiguous", ConversationID: "c1"}, col.sink)

	key := col.clarifyKey()
	if key == "" {
		t.Fatalf("no clarify resume_key emitted: %+v", col.msgs)
	}
	if paused := col.byType(messages.EventTask); len(paused) == 0 || paused[len(paused)-1].Action != "paused" {
		t.Fatalf("expected task paused, got %+v", paused)
	}

	// 2) resume -> final answer + done
	col2 := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "A", ConversationID: "c1", ResumeKey: key}, col2.sink)
	if chats := col2.byType(messages.EventChat); len(chats) == 0 || chats[len(chats)-1].Content != "resolved with A" {
		t.Fatalf("resume final missing: %+v", col2.msgs)
	}
	if done := col2.byType(messages.EventTask); len(done) == 0 || done[len(done)-1].Action != "done" {
		t.Fatalf("resume task done missing: %+v", done)
	}

	// 3) duplicate resume with same key -> rejected (checkpoint already claimed)
	col3 := &collector{}
	svc.Run(context.Background(), ChatRunRequest{Message: "A", ConversationID: "c1", ResumeKey: key}, col3.sink)
	failed := col3.byType(messages.EventTask)
	if len(failed) == 0 || failed[len(failed)-1].Action != "failed" {
		t.Fatalf("duplicate resume should fail, got %+v", col3.msgs)
	}
}

// blockingLLM blocks until the context is cancelled.
type blockingLLM struct{}

func (blockingLLM) Chat(ctx context.Context, _ llm.Request) (llm.Response, error) {
	<-ctx.Done()
	return llm.Response{}, ctx.Err()
}

func TestChat_KillCancels(t *testing.T) {
	svc, _ := testService(t, blockingLLM{})
	col := &collector{}
	done := make(chan struct{})
	go func() {
		svc.Run(context.Background(), ChatRunRequest{Message: "long task", ConversationID: "killme"}, col.sink)
		close(done)
	}()

	// wait until the run registers, then kill
	deadline := time.After(2 * time.Second)
	for {
		if svc.Kill("killme") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("run never registered for kill")
		case <-time.After(10 * time.Millisecond):
		}
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("run did not stop within 1s of kill")
	}
	failed := col.byType(messages.EventTask)
	if len(failed) == 0 || failed[len(failed)-1].Action != "failed" {
		t.Fatalf("expected task failed after kill, got %+v", col.msgs)
	}
}

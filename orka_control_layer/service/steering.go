package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

var ErrSteeringUnavailable = errors.New("当前任务尚未就绪或已结束，请保留输入并稍后重试")

// Steering changes instructions, never the active run's model, budget or grants.
type SteeringRequest struct {
	ConversationID string   `json:"conversation_id"`
	RunID          string   `json:"run_id"`
	RequestID      string   `json:"request_id"`
	Message        string   `json:"message"`
	FileIDs        []string `json:"file_ids"`
}

type steeringItem struct {
	request SteeringRequest
	message messages.Message
}

// One inbox belongs to one admitted execution. The same lock serializes acceptance
// with closing, so an acknowledged message cannot fall between two executions.
type steeringInbox struct {
	mu      sync.Mutex
	closed  bool
	pending []steeringItem
	seen    map[string]steeringItem
	accept  func(SteeringRequest) (messages.Message, error)
	cancel  adk.AgentCancelFunc
	state   []*schema.Message
}

func newSteeringInbox() *steeringInbox { return &steeringInbox{seen: make(map[string]steeringItem)} }

func steeringFrom(ctx context.Context) *steeringInbox {
	entry, _ := ctx.Value(executionAdmissionKey{}).(*executionEntry)
	if entry == nil {
		return nil
	}
	return entry.steering
}

func (s *ChatService) Steer(ctx context.Context, owner string, req SteeringRequest) (messages.Message, error) {
	if req.RequestID == "" || len(req.RequestID) > 128 || req.RunID == "" ||
		(len(strings.TrimSpace(req.Message)) == 0 && len(req.FileIDs) == 0) || len(req.Message) > 64<<10 || len(req.FileIDs) > 32 {
		return messages.Message{}, errors.New("追加消息为空或超过大小限制")
	}
	s.executions.mu.Lock()
	entry := s.executions.entries[req.RunID]
	s.executions.mu.Unlock()
	if entry == nil || entry.parentID != "" || entry.owner != owner || entry.conversation != req.ConversationID || entry.ctx.Err() != nil {
		return messages.Message{}, ErrSteeringUnavailable
	}
	if err := ctx.Err(); err != nil {
		return messages.Message{}, err
	}
	return entry.steering.push(req)
}

func (i *steeringInbox) push(req SteeringRequest) (messages.Message, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if old, ok := i.seen[req.RequestID]; ok {
		if old.request.Message != req.Message || strings.Join(old.request.FileIDs, "\x00") != strings.Join(req.FileIDs, "\x00") {
			return messages.Message{}, errors.New("重复请求的内容不一致")
		}
		return old.message, nil
	}
	if i.closed || i.accept == nil {
		return messages.Message{}, ErrSteeringUnavailable
	}
	if len(i.pending) >= 16 || len(i.seen) >= 128 {
		return messages.Message{}, errors.New("追加消息过多，请等待模型处理")
	}
	m, err := i.accept(req)
	if err != nil {
		return messages.Message{}, err
	}
	item := steeringItem{request: req, message: m}
	i.seen[req.RequestID] = item
	i.pending = append(i.pending, item)
	if i.cancel != nil {
		// Finish the in-flight tool batch; do not cancel/replay a side effect.
		i.cancel(adk.WithAgentCancelMode(adk.CancelAfterToolCalls))
	}
	return m, nil
}

func (i *steeringInbox) bind(cancel adk.AgentCancelFunc) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cancel = cancel
}

func (i *steeringInbox) close() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed, i.cancel = true, nil
}

// Called after the iterator drains. A pending instruction must be processed
// before terminal events; otherwise close atomically against the HTTP handler.
func (i *steeringInbox) finish() bool {
	if i == nil {
		return true
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.pending) > 0 {
		return false
	}
	i.closed, i.cancel = true, nil
	return true
}

func (i *steeringInbox) transcript() []*schema.Message {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]*schema.Message(nil), i.state...)
}

type steeringMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	inbox      *steeringInbox
	service    *ChatService
	rc         *agent.RunContext
	restored   []messages.Message
	restoreErr error
}

func (s *ChatService) prepareSteering(ctx context.Context, rc *agent.RunContext) *steeringMiddleware {
	i := steeringFrom(ctx)
	if i == nil {
		return nil
	}
	i.mu.Lock()
	i.accept = func(req SteeringRequest) (messages.Message, error) {
		m := humanChat(req.Message, rc.Meta)
		m.Payload = map[string]any{"file_ids": req.FileIDs, "request_id": req.RequestID}
		if err := persistSteeringRequest(ctx, m); err != nil {
			return messages.Message{}, err
		}
		rc.Emit(m)
		return m, nil
	}
	i.mu.Unlock()
	h := &steeringMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, inbox: i, service: s, rc: rc}
	if runResumeFrom(ctx) != nil || resumeFrom(ctx) != nil {
		h.restored, h.restoreErr = loadSteeringRequests(ctx)
	}
	return h
}

func (h *steeringMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if h.restoreErr != nil {
		return ctx, state, h.restoreErr
	}
	h.inbox.mu.Lock()
	items := h.inbox.pending
	h.inbox.pending = nil
	h.inbox.mu.Unlock()
	// Checkpoint state may precede acceptance of a steering message (for
	// example an approval raced with submission). Recover it exactly once.
	known := make(map[string]bool)
	for _, m := range state.Messages {
		if id, ok := m.Extra["orka_human_message_id"].(string); ok {
			known[id] = true
		}
	}
	for _, item := range items {
		known[item.message.ID] = true
	}
	for _, m := range h.restored {
		if known[m.ID] {
			continue
		}
		known[m.ID] = true
		req := SteeringRequest{Message: m.Content}
		if payload, ok := m.Payload.(map[string]any); ok {
			if paths, ok := payload["file_ids"].([]any); ok {
				for _, p := range paths {
					if path, ok := p.(string); ok {
						req.FileIDs = append(req.FileIDs, path)
					}
				}
			}
		}
		items = append(items, steeringItem{request: req, message: m})
	}
	h.restored = nil
	for _, item := range items {
		input := h.service.withAttachments(ctx, []messages.Message{item.message}, ChatRunRequest{
			Message: item.request.Message, FileIDs: item.request.FileIDs,
			UserEmail: h.rc.Meta.UserEmail, ConversationID: h.rc.Meta.ConversationID,
		}, h.rc.Meta)
		for _, m := range toEinoMessages(input) {
			state.Messages = append(state.Messages, m)
			journalFrom(ctx).append(m)
		}
	}
	if len(items) > 0 {
		journalFrom(ctx).flush()
	}
	return ctx, state, nil
}

func (h *steeringMiddleware) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.inbox.mu.Lock()
	h.inbox.state = append([]*schema.Message(nil), state.Messages...)
	h.inbox.mu.Unlock()
	return ctx, state, nil
}

// Compile-time check: state changes use Eino's native persisted-state hook.
var _ adk.ChatModelAgentMiddleware = (*steeringMiddleware)(nil)

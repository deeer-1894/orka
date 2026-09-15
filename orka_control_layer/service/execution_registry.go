package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/messages"
)

var ErrExecutionActive = errors.New("当前会话已有任务运行，请等待结束或先停止该任务")

// Execution identity is trusted internal context, never accepted from request JSON.
type executionIdentity struct{ ID, ParentID string }
type executionIdentityKey struct{}
type executionAdmissionKey struct{}

type executionEntry struct {
	id, parentID, owner, conversation, task string
	ctx                                     context.Context
	cancel                                  context.CancelFunc
}
type executionRegistry struct {
	mu      sync.Mutex
	entries map[string]*executionEntry
}

func WithExecutionIdentity(ctx context.Context, id, parentID string) context.Context {
	return context.WithValue(ctx, executionIdentityKey{}, executionIdentity{ID: id, ParentID: parentID})
}
func ExecutionID(ctx context.Context) string {
	if v, ok := ctx.Value(executionIdentityKey{}).(executionIdentity); ok {
		return v.ID
	}
	return ""
}

// AdmitExecution owns cancellation and the conversation lease. A trusted workflow
// may create independently cancellable children inside its parent admission.
// Callers must release after the execution actually ends, not when the UI detaches.
func (s *ChatService) AdmitExecution(parent context.Context, owner, conversation, task string) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	identity, _ := parent.Value(executionIdentityKey{}).(executionIdentity)
	admitted, _ := parent.Value(executionAdmissionKey{}).(*executionEntry)
	if admitted != nil {
		if admitted.owner != owner || admitted.conversation != conversation {
			return nil, nil, errors.New("execution admission identity mismatch")
		}
		if identity.ID == admitted.id {
			return parent, func() {}, nil
		}
		if identity.ParentID != admitted.id {
			return nil, nil, errors.New("invalid execution parent")
		}
	} else if identity.ParentID != "" {
		return nil, nil, errors.New("execution parent is not admitted")
	}
	if identity.ID == "" {
		identity.ID = "run_" + messages.NewID()
	}
	ctx, cancel := context.WithCancel(WithExecutionIdentity(parent, identity.ID, identity.ParentID))
	entry := &executionEntry{id: identity.ID, parentID: identity.ParentID, owner: owner, conversation: conversation, task: task, ctx: ctx, cancel: cancel}
	r := &s.executions
	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[string]*executionEntry{}
	}
	for _, active := range r.entries {
		if active.id == entry.id || (admitted == nil && conversation != "" && active.owner == owner && active.conversation == conversation) {
			r.mu.Unlock()
			cancel()
			return nil, nil, ErrExecutionActive
		}
	}
	r.entries[entry.id] = entry
	r.mu.Unlock()
	remove := func() {
		r.mu.Lock()
		if r.entries[entry.id] == entry {
			delete(r.entries, entry.id)
		}
		r.mu.Unlock()
	}
	// The database lease prevents another control process admitting the same
	// conversation. Renew failure cancels this execution before its lease expires.
	durable := admitted == nil && owner != "" && conversation != "" && s.Msg != nil && s.Msg.Store != nil && s.Msg.Store.Conversations != nil
	if durable {
		claimCtx, done := context.WithTimeout(parent, 5*time.Second)
		claimed, err := s.Msg.Store.ClaimConversationExecution(claimCtx, owner, conversation, entry.id, executionLeaseTTL)
		done()
		if err != nil || !claimed {
			remove()
			cancel()
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, ErrExecutionActive
		}
		go s.renewExecutionLease(ctx, entry)
	}
	ctx = context.WithValue(ctx, executionAdmissionKey{}, entry)
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			if durable {
				c, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				_ = s.Msg.Store.ReleaseConversationExecution(c, owner, conversation, entry.id)
			}
			remove()
		})
	}
	return ctx, release, nil
}

const executionLeaseTTL = 90 * time.Second

func (s *ChatService) renewExecutionLease(ctx context.Context, e *executionEntry) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, done := context.WithTimeout(ctx, 5*time.Second)
			ok, err := s.Msg.Store.RenewConversationExecution(c, e.owner, e.conversation, e.id, executionLeaseTTL)
			done()
			if err != nil || !ok {
				e.cancel()
				return
			}
		}
	}
}

// Kill cancels an exact execution or all active entries belonging to a task or
// conversation. Entries remain occupied until their owners release them.
func (s *ChatService) Kill(id string) bool {
	r := &s.executions
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for _, e := range r.entries {
		if e.id == id || e.conversation == id || (id != "" && e.task == id) {
			e.cancel()
			found = true
		}
	}
	return found
}

package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// steering.go — a message typed while the run is still going.
//
// Until now the composer simply dropped it: send() returned early on `busy`,
// and since the textarea was never disabled you could type a full sentence,
// press enter, and watch it vanish without a word. The only ways into a live
// run were the ones the AGENT opened — a clarify question, or the confirm gate.
// The user could not say anything the agent had not asked for.
//
// Steering makes the run listen. A message posted mid-run is held in a mailbox
// and injected into the history right before the next model call, so it lands
// in the CURRENT turn rather than queueing behind it: the model sees the
// correction on its very next step and can change course while the work is
// still in flight. That is the whole point — a correction that arrives after
// the run finishes is not a correction, it is the next task.
//
// eino gives this an exact seam. BeforeModelRewriteState is the hook that runs
// before every model call, so injection needs no change to the ADK loop and
// cannot land halfway through one. It is installed on the ORCHESTRATOR only
// (in runEino, alongside the model router). Sub-agents build their own chain,
// and draining consumes — a message injected into whichever sub-agent happened
// to call the model first would be delivered somewhere the user cannot see,
// and only there.

const (
	// steerMaxPending caps the mailbox. Beyond this the run is not keeping up
	// and the honest answer is to refuse the message, not to accept one the
	// model may see far too late.
	steerMaxPending = 8
	// steerMaxChars bounds a single message. Steering is for corrections, not
	// for pasting a new corpus into a run already in progress.
	steerMaxChars = 4000
	// steerTailRounds bounds the continuation passes. Steering lands at the
	// run's NEXT model call, and a message sent during the final one has no next
	// call to land in — so the run continues with it rather than dropping it.
	// Bounded because each pass can itself be steered, and a user typing as fast
	// as the model answers must not be able to make a run immortal.
	steerTailRounds = 3
	// steerPrefix marks the message as having arrived mid-run. Without it the
	// model reads a user turn wedged between tool results as part of the
	// original request; with it, it knows this is new information that arrived
	// after it had already started, and that it may need to abandon what it was
	// doing.
	steerPrefix = "[用户在本次任务执行过程中追加的消息]\n"
)

// steerBox is one run's mailbox. deliver is how an injected message reaches the
// transcript, and is set once at run start.
type steerBox struct {
	mu      sync.Mutex
	pending []string
	deliver func(string)
}

func newSteerBox(deliver func(string)) *steerBox {
	return &steerBox{deliver: deliver}
}

// add accepts a message for the next model call. Reports false when it was not
// accepted, which the caller must surface: the user has to learn that their
// message is not coming, and the frontend falls back to sending it as an
// ordinary turn.
func (b *steerBox) add(text string) bool {
	text = strings.TrimSpace(text)
	if b == nil || text == "" || len([]rune(text)) > steerMaxChars {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) >= steerMaxPending {
		return false
	}
	b.pending = append(b.pending, text)
	return true
}

// drain takes everything waiting and empties the box.
func (b *steerBox) drain() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.pending
	b.pending = nil
	return out
}

// waiting reports how many messages the run has not yet shown the model. Read
// at run end: anything still here was never seen, and saying so is the only way
// the caller can avoid claiming a message was delivered when it was not.
func (b *steerBox) waiting() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// steerInjector appends the mailbox's contents to the history immediately
// before the model is called.
type steerInjector struct {
	*adk.BaseChatModelAgentMiddleware
	box *steerBox
}

func newSteerInjector(box *steerBox) *steerInjector {
	return &steerInjector{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, box: box}
}

func (m *steerInjector) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	pending := m.box.drain()
	if len(pending) == 0 || state == nil {
		return ctx, state, nil
	}
	slog.Info("steer inject", "count", len(pending), "history_msgs", len(state.Messages))
	for _, text := range pending {
		state.Messages = append(state.Messages, schema.UserMessage(steerPrefix+text))
		// Announce only once it is actually in the history the model is about to
		// read. Persisting on arrival instead would let a run that ended in the
		// meantime leave a user turn in the transcript that nothing ever saw.
		if m.box.deliver != nil {
			m.box.deliver(text)
		}
	}
	return ctx, state, nil
}

type steerBoxKey struct{}

func withSteerBox(ctx context.Context, b *steerBox) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, steerBoxKey{}, b)
}

func steerBoxFrom(ctx context.Context) *steerBox {
	b, _ := ctx.Value(steerBoxKey{}).(*steerBox)
	return b
}

func (s *ChatService) registerSteer(id string, box *steerBox) {
	if id == "" {
		return
	}
	s.mu.Lock()
	if s.steers == nil {
		s.steers = map[string]*steerBox{}
	}
	s.steers[id] = box
	s.mu.Unlock()
}

func (s *ChatService) unregisterSteer(id string) {
	s.mu.Lock()
	delete(s.steers, id)
	s.mu.Unlock()
}

// Steer hands a message to a live run. Reports false when there is no run to
// hand it to, or when the run's mailbox refused it — in both cases the caller
// should send the message as a normal turn instead of dropping it.
func (s *ChatService) Steer(id, text string) bool {
	s.mu.Lock()
	box := s.steers[id]
	s.mu.Unlock()
	return box.add(text)
}

// steerDeliver builds the announcement hook for a run: an injected message is
// persisted and streamed as an ordinary user turn, so it appears in the thread
// at the point the model actually received it and survives a reload.
func (s *ChatService) steerDeliver(rc *agent.RunContext, raw func(messages.Message), meta messages.Meta) func(string) {
	return func(text string) {
		s.Msg.Deliver(rc, raw, messages.Chat(messages.RoleUser, text, meta), true)
	}
}

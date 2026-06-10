package api

import (
	"testing"

	"github.com/orka-oss/orka_core/messages"
)

func TestStreamHubReplay(t *testing.T) {
	h := newStreamHub()
	rs := h.start("conv1")

	// publish 3 events
	for i := 0; i < 3; i++ {
		h.publish("conv1", messages.Chat(messages.RoleAssistant, "m", messages.Meta{}))
	}

	// a reconnect from seq 1 should replay events 2 and 3
	_, replay, _, cancel := rs.subscribe(1)
	defer cancel()
	if len(replay) != 2 {
		t.Fatalf("expected 2 replayed frames, got %d", len(replay))
	}
	if replay[0].seq != 2 || replay[1].seq != 3 {
		t.Errorf("unexpected replay seqs: %d, %d", replay[0].seq, replay[1].seq)
	}
}

func TestStreamHubLiveDelivery(t *testing.T) {
	h := newStreamHub()
	rs := h.start("c")
	ch, _, done, cancel := rs.subscribe(0)
	defer cancel()
	if done {
		t.Fatal("run should not be done yet")
	}
	h.publish("c", messages.Chat(messages.RoleAssistant, "hi", messages.Meta{}))
	sf := <-ch
	if sf.seq != 1 {
		t.Errorf("expected seq 1, got %d", sf.seq)
	}
}

func TestStreamHubFinishClosesSubs(t *testing.T) {
	h := newStreamHub()
	rs := h.start("c")
	ch, _, _, cancel := rs.subscribe(0)
	defer cancel()
	h.finish("c")
	if _, open := <-ch; open {
		t.Error("channel should be closed after finish")
	}
}

func TestStreamHubSubscribeAfterFinish(t *testing.T) {
	h := newStreamHub()
	rs := h.start("c")
	h.publish("c", messages.Chat(messages.RoleAssistant, "x", messages.Meta{}))
	h.finish("c")
	ch, replay, done, _ := rs.subscribe(0)
	if !done {
		t.Error("subscribe after finish should report done")
	}
	if ch != nil {
		t.Error("no live channel expected after finish")
	}
	if len(replay) != 1 {
		t.Errorf("expected 1 replay frame, got %d", len(replay))
	}
}

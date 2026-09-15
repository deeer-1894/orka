package api

import (
	"github.com/orka-oss/orka_core/messages"
	"testing"
)

func TestOldStreamCannotPublishOrCloseNewExecution(t *testing.T) {
	h := newStreamHub()
	first := h.start("conv")
	second := h.start("conv")
	first.publish(messages.Message{Content: "old"})
	h.finishStream("conv", first)
	if len(second.buf) != 0 || second.done {
		t.Fatal("old execution changed new stream")
	}
	second.publish(messages.Message{Content: "new"})
	if len(first.buf) != 1 {
		t.Fatal("new event entered old execution")
	}
}
func TestStreamGapRequiresHistoryReconciliation(t *testing.T) {
	h := newStreamHub()
	s := h.start("conv")
	for i := 0; i < streamBufferSize+20; i++ {
		s.publish(messages.Message{Content: "event"})
	}
	if !s.hasGap(1) {
		t.Fatal("lost frames went undetected")
	}
	if s.hasGap(s.seq - 1) {
		t.Fatal("recent cursor should replay")
	}
	if !s.hasGap(s.seq + 1) {
		t.Fatal("cursor from another execution not detected")
	}
}

func TestStreamSubscriptionChecksCursorAtomically(t *testing.T) {
	s := newStreamHub().start("conv", "run-current")
	for i := 0; i < streamBufferSize+1; i++ {
		s.publish(messages.Message{Content: "event"})
	}
	ch, replay, _, cancel, gap := s.subscribeChecked(0, false)
	defer cancel()
	if !gap || ch != nil || len(replay) != 0 || len(s.subs) != 0 {
		t.Fatal("stale cursor attached silently")
	}
	ch, replay, _, cancel, gap = s.subscribeChecked(s.seq+100, true)
	defer cancel()
	if gap || ch == nil || len(replay) != streamBufferSize || s.executionID != "run-current" {
		t.Fatal("reconciliation lost current buffer")
	}
}

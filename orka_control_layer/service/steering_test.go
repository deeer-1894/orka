package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// The behaviour that defines steering: the message has to land in the turn
// already running, not the one after it. A run that is three tool calls deep
// must show the model the correction on its very next call.
func TestSteeredMessageEntersTheRunInProgress(t *testing.T) {
	box := newSteerBox(nil)
	if !box.add("别用 requests,这台机器没网") {
		t.Fatal("a live run refused an ordinary message")
	}

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		schema.UserMessage("抓一下这个页面"),
		{Role: schema.Tool, ToolCallID: "a", ToolName: "shell", Content: "ok"},
	}}
	_, out, err := newSteerInjector(box).BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("injection failed: %v", err)
	}
	last := out.Messages[len(out.Messages)-1]
	if last.Role != schema.User {
		t.Fatalf("the injected message is a %s turn; the model reads corrections as user turns", last.Role)
	}
	if !strings.Contains(last.Content, "没网") {
		t.Fatalf("the user's words did not survive injection: %q", last.Content)
	}
	// Without the marker the model reads a user turn wedged between tool results
	// as part of the original request, rather than as news that arrived late.
	if !strings.Contains(last.Content, steerPrefix) {
		t.Fatalf("injected message is not marked as arriving mid-run: %q", last.Content)
	}
}

// Draining consumes. The injector runs before EVERY model call, so a message
// that survived a drain would be re-appended on every subsequent call and the
// model would read one correction as many.
func TestSteeredMessageIsInjectedExactlyOnce(t *testing.T) {
	box := newSteerBox(nil)
	box.add("停下")
	inj := newSteerInjector(box)

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("go")}}
	_, state, _ = inj.BeforeModelRewriteState(context.Background(), state, nil)
	after := len(state.Messages)
	_, state, _ = inj.BeforeModelRewriteState(context.Background(), state, nil)
	if len(state.Messages) != after {
		t.Fatalf("a second model call re-injected the message (%d -> %d messages)", after, len(state.Messages))
	}
}

// A run nobody is steering must not pay for the feature, and must not have its
// history touched — rewriting it needlessly costs prefix-cache hits.
func TestInjectorLeavesAnUnsteeredRunAlone(t *testing.T) {
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("hi")}}
	_, out, err := newSteerInjector(newSteerBox(nil)).BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("history grew from 1 to %d with nothing to inject", len(out.Messages))
	}
}

// The announcement is what puts the message in the thread and in Mongo. It must
// fire when the model actually receives the message — not when the user sent
// it, or a run that ended in between would leave a user turn in the transcript
// that nothing ever read.
func TestTheMessageIsAnnouncedOnlyWhenTheModelGetsIt(t *testing.T) {
	var announced []string
	box := newSteerBox(func(s string) { announced = append(announced, s) })
	box.add("改成 csv")
	if len(announced) != 0 {
		t.Fatalf("announced %v before the model had seen anything", announced)
	}

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("go")}}
	newSteerInjector(box).BeforeModelRewriteState(context.Background(), state, nil)
	if len(announced) != 1 || announced[0] != "改成 csv" {
		t.Fatalf("announced %v, want the user's own words once", announced)
	}
	// The announcement carries the user's text, not the internal marker — the
	// bubble in the thread should read the way they typed it.
	if strings.Contains(announced[0], steerPrefix) {
		t.Fatalf("the marker leaked into the thread: %q", announced[0])
	}
}

// Refusal has to be visible. Every false here sends the frontend down the
// fallback path (send it as an ordinary turn); silently returning true would
// reproduce the exact bug this replaces — a message the user watched disappear.
func TestTheBoxRefusesRatherThanSwallows(t *testing.T) {
	box := newSteerBox(nil)
	if box.add("   ") {
		t.Error("accepted an empty message")
	}
	if box.add(strings.Repeat("x", steerMaxChars+1)) {
		t.Error("accepted a message past the size cap")
	}
	for i := 0; i < steerMaxPending; i++ {
		if !box.add("ok") {
			t.Fatalf("refused message %d, under the cap of %d", i, steerMaxPending)
		}
	}
	if box.add("one too many") {
		t.Error("accepted past the pending cap; the model would see it far too late")
	}

	// A nil box is the "no live run" case and must refuse, not panic: the event
	// path calls Steer for ids that may have finished a moment ago.
	var none *steerBox
	if none.add("x") {
		t.Error("a nil box accepted a message")
	}
	if none.waiting() != 0 || none.drain() != nil {
		t.Error("a nil box reported state")
	}
}

// waiting is how a caller learns a message was never delivered. If it lied, the
// UI would show a bubble for something the model never read.
func TestWaitingReportsTheUndelivered(t *testing.T) {
	box := newSteerBox(nil)
	box.add("a")
	box.add("b")
	if box.waiting() != 2 {
		t.Fatalf("waiting = %d, want 2", box.waiting())
	}
	if got := box.drain(); len(got) != 2 {
		t.Fatalf("drained %d, want 2", len(got))
	}
	if box.waiting() != 0 {
		t.Fatalf("waiting = %d after a drain, want 0", box.waiting())
	}
}

func TestSteerFindsTheRunByID(t *testing.T) {
	s := &ChatService{}
	box := newSteerBox(nil)
	s.registerSteer("conv-1", box)

	if !s.Steer("conv-1", "换个方向") {
		t.Fatal("a registered run did not accept a message")
	}
	if got := box.drain(); len(got) != 1 || got[0] != "换个方向" {
		t.Fatalf("the run received %v", got)
	}
	if s.Steer("conv-2", "x") {
		t.Fatal("an unknown id reported success; the message would be lost")
	}
	s.unregisterSteer("conv-1")
	if s.Steer("conv-1", "too late") {
		t.Fatal("a finished run still accepted a message")
	}
}

// Context round trip: runEino reads the box off the run context to decide
// whether to install the injector at all.
func TestSteerBoxRoundTripsThroughContext(t *testing.T) {
	if steerBoxFrom(context.Background()) != nil {
		t.Fatal("a bare context produced a box")
	}
	box := newSteerBox(nil)
	ctx := withSteerBox(context.Background(), box)
	if steerBoxFrom(ctx) != box {
		t.Fatal("the box did not survive the context")
	}
	if withSteerBox(ctx, nil) != ctx {
		t.Fatal("installing a nil box changed the context")
	}
}

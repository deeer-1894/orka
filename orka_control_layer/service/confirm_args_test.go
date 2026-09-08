package service

import (
	"context"
	"testing"
)

// The audit gap this closes: a danger tool interrupts for approval, the run is
// checkpointed, and the work finishes in a SECOND run whose tool-call map starts
// empty — so the result arrives with nothing to attach and the record reads
// `args: null`. Measured across every conversation that used the gate: 16 of 141
// danger-tool calls lost their arguments, 12 of 14 within two minutes of an
// approval. The record's whole job is to answer "what did I approve, and what
// then ran".
func TestConfirmedArgsSurviveTheResume(t *testing.T) {
	c := newConfirmedArgs()
	args := map[string]any{"command": "curl -sL -o SHA256_nist.pdf https://csrc.nist.gov/..."}
	c.put("shell", args)

	got := c.take("shell")
	if got == nil {
		t.Fatal("an approved call's arguments were not recoverable")
	}
	if got["command"] != args["command"] {
		t.Fatalf("recovered the wrong command: %v", got["command"])
	}
}

// Taking must consume: a second call of the same tool in the same run has its
// own arguments, and inheriting the first one's record would be worse than
// recording nothing — it would attribute one command to another.
func TestConfirmedArgsAreConsumedOnce(t *testing.T) {
	c := newConfirmedArgs()
	c.put("shell", map[string]any{"command": "first"})
	if got := c.take("shell"); got == nil || got["command"] != "first" {
		t.Fatalf("first take = %v", got)
	}
	if got := c.take("shell"); got != nil {
		t.Fatalf("a second take returned %v; the record would be attributed to the wrong call", got)
	}
}

func TestConfirmedArgsKeepsToolsApart(t *testing.T) {
	c := newConfirmedArgs()
	c.put("shell", map[string]any{"command": "ls"})
	c.put("http_request", map[string]any{"url": "https://example.com"})
	if got := c.take("shell"); got["command"] != "ls" {
		t.Fatalf("shell got %v", got)
	}
	if got := c.take("http_request"); got["url"] != "https://example.com" {
		t.Fatalf("http_request got %v", got)
	}
}

// Every accessor has to tolerate absence: the gate is not installed on headless
// runs, and the event path consults it on EVERY tool result.
func TestConfirmedArgsNilAndMissingAreInert(t *testing.T) {
	var c *confirmedArgs
	c.put("shell", map[string]any{"command": "x"})
	if got := c.take("shell"); got != nil {
		t.Fatalf("nil store returned %v", got)
	}
	if got := confirmedArgsFrom(context.Background()); got != nil {
		t.Fatalf("a context with no store returned %v", got)
	}
	real := newConfirmedArgs()
	if got := real.take("never-called"); got != nil {
		t.Fatalf("unknown tool returned %v", got)
	}
	real.put("", map[string]any{"a": 1})
	real.put("shell", nil)
	if got := real.take("shell"); got != nil {
		t.Fatalf("a nil argument map was stored as %v", got)
	}
}

func TestConfirmedArgsRoundTripThroughContext(t *testing.T) {
	c := newConfirmedArgs()
	ctx := withConfirmedArgs(context.Background(), c)
	confirmedArgsFrom(ctx).put("python", map[string]any{"code": "print(1)"})
	if got := c.take("python"); got == nil || got["code"] != "print(1)" {
		t.Fatalf("context round trip lost the arguments: %v", got)
	}
	// A nil store must not replace one already installed.
	if withConfirmedArgs(ctx, nil) != ctx {
		t.Fatal("installing a nil store changed the context")
	}
}

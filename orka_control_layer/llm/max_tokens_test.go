package llm

import "testing"

// The cap is applied at the CLIENT, not at the eight places an eino model is
// built, so this is the one check that it reaches the wire at all.
func TestTheClientCapAppliesWhenTheCallerSetsNone(t *testing.T) {
	c := &OpenAIClient{DefaultMaxTokens: 16384}
	if got := c.wireRequestFor(Request{Model: "m"}).MaxTokens; got != 16384 {
		t.Fatalf("max_tokens = %d, want the client default", got)
	}
}

// A caller that chose its own cap keeps it: followups asks for 800 tokens and
// must not be handed a budget twenty times that.
func TestACallersOwnCapWins(t *testing.T) {
	c := &OpenAIClient{DefaultMaxTokens: 16384}
	if got := c.wireRequestFor(Request{Model: "m", MaxTokens: 800}).MaxTokens; got != 800 {
		t.Fatalf("max_tokens = %d, want the caller's 800", got)
	}
}

// Zero is the safe default and must stay off the wire entirely: too low a cap
// truncates a legitimately large single deliverable, and omitting the field
// leaves the provider's own ceiling in place.
func TestNoCapConfiguredSendsNoCap(t *testing.T) {
	c := &OpenAIClient{}
	if got := c.wireRequestFor(Request{Model: "m"}).MaxTokens; got != 0 {
		t.Fatalf("max_tokens = %d, want 0 (omitted)", got)
	}
}

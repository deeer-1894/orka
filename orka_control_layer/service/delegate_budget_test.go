package service

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func usedMsg(tok int) *schema.Message {
	m := schema.AssistantMessage("step", nil)
	m.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: tok}}
	return m
}

// A delegate agent is built once per run and its budget guard is shared by every
// invocation of it. With a sticky budget the first delegation to spend its
// allowance poisoned the object for the whole run: later delegations entered
// already-exhausted, lost their tools on cycle one, and returned status reports
// saying so — "本轮对话在任务启动后立即被系统终止" from four delegates in one run,
// after which the orchestrator did the work itself and re-fetched 27% of the
// URLs its delegates had already read.
func TestDelegateBudgetDoesNotLeakAcrossDelegations(t *testing.T) {
	b := newDelegateBudget(12, 80_000)

	// First delegation burns its allowance.
	var first []*schema.Message
	for i := 0; i < 3; i++ {
		first = append(first, usedMsg(30_000))
	}
	if !b.observe(first) {
		t.Fatal("a delegation over its token allowance should exhaust")
	}
	if b.exhausted() != "tokens" {
		t.Fatalf("exhausted reason = %q, want tokens", b.exhausted())
	}

	// SECOND delegation: a fresh message list on the same shared object. It must
	// start with its full allowance, not inherit the first one's exhaustion.
	second := []*schema.Message{usedMsg(500)}
	if b.observe(second) {
		t.Fatal("a new delegation inherited the previous one's exhausted budget — " +
			"its tools would be stripped on the first cycle and it would return " +
			"a status report having done nothing")
	}
	if b.exhausted() != "" {
		t.Errorf("exhaustion state leaked into a fresh delegation: %q", b.exhausted())
	}
}

// Within ONE delegation the guard must still latch: the total only grows, so a
// delegate that blew its budget does not get its tools back mid-flight.
func TestDelegateBudgetStillBoundsOneDelegation(t *testing.T) {
	b := newDelegateBudget(12, 80_000)
	msgs := []*schema.Message{usedMsg(50_000)}
	if b.observe(msgs) {
		t.Fatal("tripped early")
	}
	msgs = append(msgs, usedMsg(50_000))
	if !b.observe(msgs) {
		t.Fatal("a delegation past its allowance must lose its tools")
	}
}

// The RUN budget keeps its stickiness, and must: it exists to survive
// summarization collapsing the message list, which would otherwise revive a
// spent budget. Delegates run no summarizer, which is why non-sticky is safe
// there and only there.
func TestRunBudgetStaysSticky(t *testing.T) {
	b := newRunBudget(200, 80_000, 0)
	// Spend is reported by the LLM client, not read off the messages.
	b.AddUsage(85_000, 5_000)
	if !b.observe([]*schema.Message{usedMsg(90_000)}) {
		t.Fatal("run budget should exhaust")
	}
	// Summarization collapses the history; the budget must NOT come back.
	if !b.observe([]*schema.Message{usedMsg(10)}) {
		t.Error("run budget revived after the message list shrank")
	}
	if b.exhausted() != "tokens" {
		t.Errorf("run budget lost its reason: %q", b.exhausted())
	}
}

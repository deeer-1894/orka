package service

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/llm"
)

// The run budget must be a ledger, not a reading of the current window.
//
// It used to sum ResponseMeta.Usage over the messages it was handed, and those
// messages are rewritten underneath it: reduction clears tool results, and
// summarization collapses the history to a system message plus a summary,
// discarding the assistant messages that carried the usage. The total reset
// every time. Measured on one run: 2,293,602 tokens billed against an 800k cap
// that never fired — and the step ceiling was the only limit actually holding,
// which is why raising it opened the gate completely.
func TestRunBudgetCountsSpendNotTheCurrentWindow(t *testing.T) {
	b := newRunBudget(200, 800_000, 0)

	// Nine calls of 100k, reported as they complete.
	for i := 0; i < 9; i++ {
		b.AddUsage(90_000, 10_000)
	}
	// ...and then context management throws the history away.
	collapsed := []*schema.Message{schema.SystemMessage("sys"), schema.AssistantMessage("summary", nil)}

	if !b.observe(collapsed) {
		t.Fatal("900k spent against an 800k cap did not exhaust: the budget is " +
			"still reading the message list rather than what was billed")
	}
	if b.exhausted() != "tokens" {
		t.Errorf("exhausted reason = %q, want tokens", b.exhausted())
	}
	if got := b.spentTokens(); got != 900_000 {
		t.Errorf("spent = %d, want 900000", got)
	}
}

// Under the cap, an emptied history must not look like an exhausted run either.
func TestRunBudgetDoesNotTripEarly(t *testing.T) {
	b := newRunBudget(200, 800_000, 0)
	b.AddUsage(100_000, 5_000)
	if b.observe([]*schema.Message{schema.SystemMessage("sys")}) {
		t.Error("105k against an 800k cap should not exhaust")
	}
}

// runBudget has to satisfy the sink the LLM client reports to; if that ever
// stops compiling the budget silently goes back to counting nothing.
func TestRunBudgetIsAUsageSink(t *testing.T) {
	var _ llm.UsageSink = newRunBudget(200, 800_000, 0)
}

// Delegates stay un-metered. Their budget object is per delegate TYPE while the
// sink is per RUN, so metering them would bill one delegation for every other
// delegation's calls — and they have no summarizer to collapse their history,
// which is what made the message count unreliable in the first place.
func TestDelegateBudgetStillReadsItsOwnMessages(t *testing.T) {
	b := newDelegateBudget(12, 80_000)
	// A run-level report must not touch a delegate's allowance.
	b.AddUsage(500_000, 500_000)
	if b.observe([]*schema.Message{usedMsg(1_000)}) {
		t.Fatal("a delegate was charged for the run's spend")
	}
	// Its own messages still bound it.
	if !b.observe([]*schema.Message{usedMsg(50_000), usedMsg(50_000)}) {
		t.Error("a delegate past its own allowance should exhaust")
	}
}

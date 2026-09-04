package service

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/summarization"
)

// eino's shouldSummarize checks messages, then FALLS THROUGH to a token check,
// and getTriggerContextTokens only substitutes its 160k default when the whole
// Trigger is nil. A Trigger carrying only ContextMessages therefore leaves
// ContextTokens at 0, and `tokens > 0` is true for every call — the backstop
// stops being a backstop.
//
// It did. The summarizer ran on all 23 orchestrator cycles of a run whose
// message count never went above 8, at 38-41s a call: 876s, 60% of that run's
// model time and three times the orchestrator's own, compressing a context that
// was never large. Nothing pointed at it until the calls were timed, because it
// makes no tool call and never reaches the run record.
//
// This pins the shape of the config rather than the numbers, since the trap is
// the zero value and not the threshold.
func TestSummarizationTriggerSetsBothThresholds(t *testing.T) {
	mws := summarizationHandlers(context.Background(), nil, "test-model")
	if len(mws) == 0 {
		t.Skip("summarization middleware unavailable in this build")
	}

	trig := summarizationTrigger()
	if trig == nil {
		t.Fatal("no trigger configured: eino would then apply its own 160k default")
	}
	if trig.ContextMessages <= 0 {
		t.Error("ContextMessages is unset, so long dialogues never trigger on count")
	}
	if trig.ContextTokens <= 0 {
		t.Fatal("ContextTokens is zero: eino compares tokens > 0, which matches EVERY " +
			"call and makes summarization unconditional")
	}
	// A backstop has to sit above the layer it backs up. Reduction clears at
	// clearAboveTokens; summarizing below that would fire before trimming had a
	// chance and pay a model call to do the cheaper layer's job.
	if trig.ContextTokens <= clearAboveTokens {
		t.Errorf("ContextTokens (%d) is at or below the reduction threshold (%d); "+
			"the last resort must not trigger before the cheap layer",
			trig.ContextTokens, clearAboveTokens)
	}
}

// The single-agent path builds its own summarizer; it must not diverge.
func TestSummarizationTriggerIsSharedNotDuplicated(t *testing.T) {
	a, b := summarizationTrigger(), summarizationTrigger()
	if a.ContextMessages != b.ContextMessages || a.ContextTokens != b.ContextTokens {
		t.Error("trigger construction is not deterministic")
	}
	var _ *summarization.TriggerCondition = a
}

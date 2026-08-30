package service

import (
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/messages"
)

func assistantWithTokens(n int) *schema.Message {
	m := schema.AssistantMessage("...", nil)
	if n > 0 {
		m.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: n}}
	}
	return m
}

// A message without usage metadata must not panic — most providers omit it on
// streamed chunks, and an earlier version dereferenced it unconditionally, which
// crashed every run.
func TestBudgetIgnoresMissingUsage(t *testing.T) {
	b := newRunBudget(8, 1000, 0)
	if b.observe([]*schema.Message{schema.UserMessage("hi"), assistantWithTokens(0), nil}) {
		t.Fatal("budget tripped on a run that has barely started")
	}
	if _, tok := b.usage(); tok != 0 {
		t.Fatalf("tokens = %d, want 0 when no usage is reported", tok)
	}
}

func TestBudgetTripsOnSteps(t *testing.T) {
	b := newRunBudget(3, 0, 0)
	// maxSteps-1 leaves the final cycle tool-free so the model can still answer.
	if b.observe([]*schema.Message{assistantWithTokens(0)}) {
		t.Fatal("tripped at 1 of 3 steps")
	}
	if !b.observe([]*schema.Message{assistantWithTokens(0), assistantWithTokens(0)}) {
		t.Fatal("did not trip at the last allowed step")
	}
	if got := b.exhausted(); got != "steps" {
		t.Fatalf("exhausted = %q, want steps", got)
	}
}

func TestBudgetTripsOnTokens(t *testing.T) {
	b := newRunBudget(0, 500, 0)
	if b.observe([]*schema.Message{assistantWithTokens(200)}) {
		t.Fatal("tripped under the token ceiling")
	}
	if !b.observe([]*schema.Message{assistantWithTokens(200), assistantWithTokens(400)}) {
		t.Fatal("did not trip over the token ceiling")
	}
	if got := b.exhausted(); got != "tokens" {
		t.Fatalf("exhausted = %q, want tokens", got)
	}
}

func TestBudgetTripsOnWallClock(t *testing.T) {
	b := newRunBudget(0, 0, time.Hour)
	if b.observe([]*schema.Message{assistantWithTokens(0)}) {
		t.Fatal("tripped while the deadline is an hour away")
	}
	b.deadline = time.Now().Add(-time.Second) // as if the hour had elapsed
	if !b.observe([]*schema.Message{assistantWithTokens(0)}) {
		t.Fatal("did not trip on an expired deadline")
	}
	if got := b.exhausted(); got != "time" {
		t.Fatalf("exhausted = %q, want time", got)
	}
}

// A zero or negative allowance means "unlimited", not "already spent" — the
// distinction matters because sub-agents and tests construct budgets with
// dimensions they do not want enforced.
func TestBudgetZeroDimensionsAreUnlimited(t *testing.T) {
	b := newRunBudget(0, 0, 0)
	for i := 0; i < 50; i++ {
		if b.observe([]*schema.Message{assistantWithTokens(100_000)}) {
			t.Fatal("an unlimited budget tripped")
		}
	}
}

// eino replays the same state when it retries or fails over a model call.
// Counting from the message list (rather than incrementing) must therefore keep
// the tally stable, or a flaky provider would silently eat the budget.
func TestBudgetDoesNotAdvanceOnReplay(t *testing.T) {
	b := newRunBudget(10, 0, 0)
	state := []*schema.Message{assistantWithTokens(10), assistantWithTokens(10)}
	for i := 0; i < 5; i++ {
		b.observe(state)
	}
	if steps, _ := b.usage(); steps != 2 {
		t.Fatalf("steps = %d after 5 replays of a 2-step state, want 2", steps)
	}
}

// Once exhausted a budget must stay exhausted: the guard has already stripped
// the tools and told the model to wrap up, so un-tripping would hand tools back
// mid-sentence.
func TestBudgetNeverUntrips(t *testing.T) {
	b := newRunBudget(2, 0, 0)
	if !b.observe([]*schema.Message{assistantWithTokens(0)}) {
		t.Fatal("expected trip")
	}
	if !b.observe(nil) || b.exhausted() != "steps" {
		t.Fatal("budget un-tripped on a smaller state")
	}
}

func TestBudgetNilIsInert(t *testing.T) {
	var b *runBudget
	if b.observe([]*schema.Message{assistantWithTokens(1)}) || b.exhausted() != "" {
		t.Fatal("nil budget must never trip")
	}
	if s, tok := b.usage(); s != 0 || tok != 0 {
		t.Fatal("nil budget must report no usage")
	}
}

func TestPlanTrackerReportsUnfinished(t *testing.T) {
	p := &planTracker{}
	if got := p.unfinished(); got != nil {
		t.Fatalf("a run with no published plan has nothing to check, got %v", got)
	}
	p.record([]messages.PlanStep{
		{Title: "读研报", Status: "done"},
		{Title: "抽因子", Status: "active"},
		{Title: "回测", Status: "pending"},
	})
	got := p.unfinished()
	if len(got) != 2 || got[0] != "抽因子" || got[1] != "回测" {
		t.Fatalf("unfinished = %v, want the active and pending steps", got)
	}
	// A later snapshot REPLACES the plan (update_plan is idempotent), so a
	// completed run must report nothing left over.
	p.record([]messages.PlanStep{
		{Title: "读研报", Status: "done"},
		{Title: "抽因子", Status: "done"},
		{Title: "回测", Status: "done"},
	})
	if got := p.unfinished(); got != nil {
		t.Fatalf("unfinished = %v, want none after every step is done", got)
	}
}

func TestPlanTrackerNilIsInert(t *testing.T) {
	var p *planTracker
	p.record([]messages.PlanStep{{Title: "x", Status: "pending"}})
	if got := p.unfinished(); got != nil {
		t.Fatalf("nil tracker returned %v", got)
	}
}

func TestBudgetNoticeNamesTheDimension(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"steps", "步数"}, {"tokens", "token"}, {"time", "时间"},
	} {
		m := budgetNotice(tc.reason)
		if m.Role != schema.User {
			t.Fatalf("notice role = %v; a mid-conversation system turn is ignored by some providers", m.Role)
		}
		if !contains(m.Content, tc.want) {
			t.Fatalf("notice for %q does not mention %q: %s", tc.reason, tc.want, m.Content)
		}
		if !contains(m.Content, "不要编造") {
			t.Fatalf("notice for %q omits the instruction not to fabricate", tc.reason)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHumanCount(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{{999, "999"}, {1500, "2k"}, {143789, "144k"}, {5_000_000, "5.0M"}} {
		if got := humanCount(tc.in); got != tc.want {
			t.Errorf("humanCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel stands in for a tier; only identity matters to the router.
type fakeModel struct{ name string }

func (f *fakeModel) Generate(context.Context, []*schema.Message, ...einomodel.Option) (*schema.Message, error) {
	return schema.AssistantMessage(f.name, nil), nil
}
func (f *fakeModel) Stream(context.Context, []*schema.Message, ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func assistants(n int) []*schema.Message {
	out := make([]*schema.Message, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, schema.AssistantMessage("x", nil))
	}
	return out
}

// which reports the tier the router would serve right now.
func which(t *testing.T, r *modelRouter) string {
	t.Helper()
	m, err := r.WrapModel(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := m.(*fakeModel)
	if !ok {
		t.Fatalf("router returned %T", m)
	}
	return f.name
}

func step(t *testing.T, r *modelRouter, n int) {
	t.Helper()
	if _, _, err := r.BeforeModelRewriteState(context.Background(),
		&adk.ChatModelAgentState{Messages: assistants(n)}, nil); err != nil {
		t.Fatal(err)
	}
}

func newTestRouter(strongFirst bool) *modelRouter {
	return newModelRouter(&fakeModel{"fast"}, "fast", &fakeModel{"strong"}, "strong", strongFirst)
}

// The whole point: a simple question never pays for the strong model, because
// it finishes before the router has any reason to escalate.
func TestRouterStartsFastAndStaysThereForShortRuns(t *testing.T) {
	r := newTestRouter(false)
	step(t, r, 0)
	if got := which(t, r); got != "fast" {
		t.Fatalf("first call served %q, want fast", got)
	}
	step(t, r, 1)
	if got := which(t, r); got != "fast" {
		t.Fatalf("a one-cycle answer escalated to %q", got)
	}
	if name, esc := r.chosen(); name != "fast" || esc {
		t.Fatalf("chosen = %q/%v, want fast/false", name, esc)
	}
}

// A run still going after several cycles has proven it is not simple, and the
// rest of it runs on the strong model — no classifier call required.
func TestRouterEscalatesOnEvidence(t *testing.T) {
	r := newTestRouter(false)
	step(t, r, autoEscalateAfter-1)
	if got := which(t, r); got != "fast" {
		t.Fatalf("escalated one cycle early (%q)", got)
	}
	step(t, r, autoEscalateAfter)
	if got := which(t, r); got != "strong" {
		t.Fatalf("did not escalate after %d cycles (%q)", autoEscalateAfter, got)
	}
	name, esc := r.chosen()
	if name != "strong" || !esc {
		t.Fatalf("chosen = %q/%v, want strong/true", name, esc)
	}
}

// Escalation is one-way. Dropping back mid-run would swap models between a tool
// call and the turn that reads its result.
func TestRouterNeverDeEscalates(t *testing.T) {
	r := newTestRouter(false)
	step(t, r, autoEscalateAfter)
	if which(t, r) != "strong" {
		t.Fatal("expected escalation")
	}
	step(t, r, 0) // e.g. history compacted away
	if got := which(t, r); got != "strong" {
		t.Fatalf("de-escalated to %q mid-run", got)
	}
}

// A request that is plainly big skips the cheap start, so it does not waste
// three cycles of the weak model discovering what the prompt already said.
func TestRouterStrongFirstSkipsTheCheapStart(t *testing.T) {
	r := newTestRouter(true)
	step(t, r, 0)
	if got := which(t, r); got != "strong" {
		t.Fatalf("strongFirst served %q", got)
	}
	// It was chosen, not escalated — the distinction the run record reports.
	if _, esc := r.chosen(); esc {
		t.Error("an up-front choice was recorded as an escalation")
	}
}

func TestRouteStrongFirstSignals(t *testing.T) {
	complex := []string{
		"调研并对比三个 AI Agent 框架",
		"写一份关于新能源汽车的报告",
		"Compare LangGraph and CrewAI in depth",
		"1) 查资料 2) 整理成表 3) 写结论 4) 存档",
		strings.Repeat("这是一个很长的请求。", 30),
	}
	for _, m := range complex {
		if !routeStrongFirst(m) {
			t.Errorf("treated as simple: %.40s", m)
		}
	}
	simple := []string{
		"什么是幂等性?",
		"现在几点",
		"1+1 等于几",
		"What is a monad?",
	}
	for _, m := range simple {
		if routeStrongFirst(m) {
			t.Errorf("treated as complex: %q", m)
		}
	}
}

// A deployment with one model must not pretend to route.
func TestRouterDegradesWithOneTier(t *testing.T) {
	r := newModelRouter(&fakeModel{"only"}, "only", nil, "", false)
	step(t, r, autoEscalateAfter+5)
	if got := which(t, r); got != "only" {
		t.Fatalf("served %q with no strong tier", got)
	}
	if name, esc := r.chosen(); name != "only" || esc {
		t.Fatalf("chosen = %q/%v", name, esc)
	}
}

func TestRouterNilIsInert(t *testing.T) {
	var r *modelRouter
	if name, esc := r.chosen(); name != "" || esc {
		t.Fatalf("nil router reported %q/%v", name, esc)
	}
}

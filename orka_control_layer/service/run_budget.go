package service

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/messages"
)

// This file gives a run a BUDGET and a DEFINITION OF DONE — the two things a
// long-running agent needs that a short one can ignore.
//
// Before this, the only limit was a step count, enforced by stripping the tools
// on the last allowed cycle. That made the model answer from whatever it had,
// which is right for a run that is nearly finished and badly wrong for one that
// is half-way through: it produced a confident-looking conclusion built on
// incomplete work, and the run was still recorded as "done". Measured on this
// deployment: 12 runs hit that cliff, and every one of them was filed as a
// success.
//
// The budget here is three-dimensional (steps / tokens / wall clock) because
// those fail independently — a run can be cheap but endless, or short but
// enormous. When any dimension runs out the agent still loses its tools (that
// part worked), but it is now also TOLD it is out of budget and instructed to
// report honestly, and the run is marked so the caller can file it as partial
// rather than done.

// runBudget is one run's remaining allowance. Safe for concurrent use: sub-agent
// middlewares consult it from their own goroutines.
type runBudget struct {
	maxSteps  int // model generation cycles (0 = unlimited)
	maxTokens int // cumulative tokens across the run (0 = unlimited)
	deadline  time.Time

	mu     sync.Mutex
	steps  int
	tokens int
	hit    string // "" until exhausted, then steps | tokens | time
}

func newRunBudget(maxSteps, maxTokens int, wall time.Duration) *runBudget {
	b := &runBudget{maxSteps: maxSteps, maxTokens: maxTokens}
	if wall > 0 {
		b.deadline = time.Now().Add(wall)
	}
	return b
}

// observe records the state of the conversation before a model call and reports
// whether the budget is now exhausted. Counting from the message list (rather
// than incrementing a counter) keeps it correct when eino retries or fails over
// a call: those replay the same state instead of advancing it.
func (b *runBudget) observe(msgs []*schema.Message) bool {
	if b == nil {
		return false
	}
	steps, tokens := 0, 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.Role == schema.Assistant {
			steps++
		}
		// Both are optional: only a completed model response carries usage, and
		// providers may omit it entirely. Missing usage means this dimension
		// simply doesn't bind — steps and wall clock still do.
		if m.ResponseMeta != nil && m.ResponseMeta.Usage != nil {
			tokens += m.ResponseMeta.Usage.TotalTokens
		}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.steps, b.tokens = steps, tokens
	if b.hit != "" {
		return true // stay exhausted; never un-trip
	}
	switch {
	// -1 leaves the final cycle free of tools so the model can still answer.
	case b.maxSteps > 1 && steps >= b.maxSteps-1:
		b.hit = "steps"
	case b.maxTokens > 0 && tokens >= b.maxTokens:
		b.hit = "tokens"
	case !b.deadline.IsZero() && time.Now().After(b.deadline):
		b.hit = "time"
	}
	return b.hit != ""
}

// exhausted reports which dimension ran out ("" = still within budget).
func (b *runBudget) exhausted() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hit
}

func (b *runBudget) usage() (steps, tokens int) {
	if b == nil {
		return 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.steps, b.tokens
}

// budgetNotice is appended to the model's context on the final cycle. It is a
// user message rather than a system one because mid-conversation system turns
// are ignored or down-weighted by several providers, and this instruction has to
// land — it is the only thing standing between "out of budget" and a fabricated
// conclusion.
func budgetNotice(reason string) *schema.Message {
	why := map[string]string{
		"steps":  "已达到本轮允许的最大步数",
		"tokens": "已达到本轮允许的最大 token 用量",
		"time":   "已达到本轮允许的最长运行时间",
	}[reason]
	if why == "" {
		why = "已达到本轮预算上限"
	}
	return schema.UserMessage("[系统] " + why + ",工具已停用,这是最后一次回复。\n" +
		"不要编造未完成的结论。请如实汇报:\n" +
		"1. 已经完成了什么(附已产出的文件/结果)\n" +
		"2. 还剩什么没做\n" +
		"3. 若要继续,下一步应该做什么\n" +
		"用户可以据此让你继续。")
}

// ---- plan completion ----

// planTracker keeps the latest checklist the agent published via update_plan, so
// a run can be checked against what it said it would do. Without it "done" means
// only "the model stopped calling tools", which is not a completion signal at
// all — it is equally true of a finished run and an abandoned one.
type planTracker struct {
	mu    sync.Mutex
	steps []messages.PlanStep
}

func (p *planTracker) record(steps []messages.PlanStep) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.steps = append([]messages.PlanStep(nil), steps...)
	p.mu.Unlock()
}

// unfinished returns the titles of steps still pending or active. A plan the
// agent never published yields nothing, which correctly means "no claim to
// check" rather than "incomplete".
func (p *planTracker) unfinished() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, s := range p.steps {
		if s.Status != "done" {
			out = append(out, s.Title)
		}
	}
	return out
}

// agentBudget returns the run-scoped budget when the caller installed one (so
// the orchestrator's step/token/time allowance is the RUN's, and whoever
// finalizes the run can read what was spent), falling back to a private
// step-only budget for the test and sub-agent paths that don't set one.
func agentBudget(ctx context.Context, maxSteps int) *runBudget {
	if b := budgetFrom(ctx); b != nil {
		return b
	}
	return newRunBudget(maxSteps, 0, 0)
}

// ---- context carriers ----

type budgetKey struct{}
type planKey struct{}

func withBudget(ctx context.Context, b *runBudget) context.Context {
	if b == nil {
		return ctx
	}
	return context.WithValue(ctx, budgetKey{}, b)
}

func budgetFrom(ctx context.Context) *runBudget {
	b, _ := ctx.Value(budgetKey{}).(*runBudget)
	return b
}

func withPlanTracker(ctx context.Context, p *planTracker) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, planKey{}, p)
}

func planTrackerFrom(ctx context.Context) *planTracker {
	p, _ := ctx.Value(planKey{}).(*planTracker)
	return p
}

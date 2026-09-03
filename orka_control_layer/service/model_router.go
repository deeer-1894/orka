package service

import (
	"context"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// model_router.go — pick the model per RUN, and change your mind mid-run.
//
// The platform had exactly two choices, both made before the first token: the
// main model or the "mini" one, chosen by hand in the UI. That is the wrong
// moment to decide, because the thing that determines which model a task needs
// — how hard the task turns out to be — is not known until the work starts.
//
// The obvious implementation is to ask a model to classify the request first.
// That is rejected here: it costs a full round-trip before any work begins, and
// on this deployment a round-trip has a MEASURED median of 44 seconds (p90 95s,
// across 176 runs). A classifier would be one of the most expensive things in
// the system, paid on every single turn including the trivial ones.
//
// So routing uses only free signals:
//
//   - Start cheap. A question that finishes in one model cycle never needed the
//     strong model, and most do finish in one.
//   - Escalate on evidence. A run still going after several cycles has proven
//     it is not a simple question, and the rest of it runs on the strong model.
//   - Skip the cheap start when the request is plainly big. Reading the prompt
//     costs nothing, and a request that already lists numbered steps or asks for
//     research is not going to finish in one cycle.
//
// eino supports the mid-run swap directly: a middleware's WrapModel may return
// a different model for the next call.

const (
	// autoEscalateAfter is how many model cycles the fast model gets before the
	// run moves to the strong one. Simple questions finish in one; anything
	// still going after three has demonstrated it is not simple.
	autoEscalateAfter = 3
	// autoLongPrompt is the character count past which a request is treated as
	// complex without waiting for evidence. Set high enough that ordinary
	// questions — even wordy ones — stay on the fast model.
	autoLongPrompt = 220
)

// ModelAuto is the request's selected_version value for automatic routing.
const ModelAuto = "auto"

// complexMarkers are phrases that reliably precede multi-step work. Matching is
// substring-based and deliberately conservative: a false positive costs the
// price difference on one run, a false negative costs only a late escalation.
var complexMarkers = []string{
	"调研", "研报", "对比", "综述", "报告", "分析一下", "写一份", "详细",
	"逐步", "依次", "分别", "多步", "计划",
	"research", "compare", "report", "analyze", "step by step", "in depth",
}

// routeStrongFirst reports whether a request is obviously too big to bother
// starting on the fast model. Pure string inspection — no model call.
func routeStrongFirst(message string) bool {
	if len([]rune(message)) >= autoLongPrompt {
		return true
	}
	low := strings.ToLower(message)
	for _, m := range complexMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	// A request that enumerates its own steps ("1) … 2) …") is multi-step by
	// construction, however briefly it is worded.
	return strings.Count(message, ")") >= 3 || strings.Count(message, "、") >= 4
}

// modelRouter serves the model for each call of one run, escalating from fast to
// strong when the run turns out to need it.
type modelRouter struct {
	*adk.BaseChatModelAgentMiddleware
	fast, strong         einomodel.BaseChatModel
	fastName, strongName string

	mu        sync.Mutex
	strongNow bool
	steps     int
	escalated bool // strongNow was reached by escalation, not by the initial choice
}

// newModelRouter builds a router. strongFirst skips the cheap start. A nil fast
// or strong model collapses the router to whichever one exists, so a
// single-model deployment keeps working.
func newModelRouter(fast einomodel.BaseChatModel, fastName string, strong einomodel.BaseChatModel, strongName string, strongFirst bool) *modelRouter {
	r := &modelRouter{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		fast:                         fast, fastName: fastName,
		strong: strong, strongName: strongName,
		strongNow: strongFirst || fast == nil,
	}
	if strong == nil {
		r.strongNow = false
	}
	return r
}

// BeforeModelRewriteState counts cycles and escalates once the run has shown it
// is not a one-shot question.
func (r *modelRouter) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	n := 0
	for _, m := range state.Messages {
		if m != nil && m.Role == schema.Assistant {
			n++
		}
	}
	r.mu.Lock()
	r.steps = n
	if !r.strongNow && r.strong != nil && n >= autoEscalateAfter {
		r.strongNow, r.escalated = true, true
	}
	r.mu.Unlock()
	return ctx, state, nil
}

// WrapModel substitutes the tier this call should run on. eino calls it per
// model invocation, which is what makes a mid-run change possible at all.
func (r *modelRouter) WrapModel(_ context.Context, m einomodel.BaseChatModel, _ *adk.ModelContext) (einomodel.BaseChatModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.strongNow {
		if r.strong != nil {
			return r.strong, nil
		}
		return m, nil
	}
	if r.fast != nil {
		return r.fast, nil
	}
	return m, nil
}

// varModelRouter carries the run's router so finalizeRun can record which tier
// actually served the work.
const varModelRouter = "model_router"

// routerFor builds the router for a run, or nil when routing is not requested.
// Only ModelAuto routes: an explicit pick is the user's decision and must not be
// second-guessed, and the fixed tiers are already explicit.
func (s *ChatService) routerFor(rc *agent.RunContext, startModel string) *modelRouter {
	if rc == nil || rc.Meta.ModelVersion != ModelAuto {
		return nil
	}
	fastClient, fastName := s.modelFor(ModelAuto)
	strongClient, strongName := s.strongModelFor(ModelAuto)
	if fastClient == nil || strongClient == nil || fastName == strongName {
		return nil // nothing to route between
	}
	// The prompt is the last user message of this turn.
	var prompt string
	for i := len(rc.Messages) - 1; i >= 0; i-- {
		if rc.Messages[i].Role == messages.RoleUser {
			prompt = rc.Messages[i].Content
			break
		}
	}
	return newModelRouter(
		llm.NewEinoModel(fastClient, fastName).ForAgent("router-fast"), fastName,
		llm.NewEinoModel(strongClient, strongName).ForAgent("router-strong"), strongName,
		routeStrongFirst(prompt),
	)
}

// chosen reports the tier in use and whether it was reached by escalation, for
// the run record and the UI.
func (r *modelRouter) chosen() (name string, escalated bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.strongNow {
		return r.strongName, r.escalated
	}
	return r.fastName, false
}

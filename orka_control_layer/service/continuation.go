package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/orka-oss/orka_control_layer/llm"
)

// continuation.go — deciding whether a run that stopped is actually finished.
//
// Long tasks here do not crash. Measured across the 122 runs since the tool
// boundary was fixed, success falls off a cliff with the amount of work asked
// for — and almost none of it is failure:
//
//	tool calls   runs   done
//	0-3            88    84%
//	4-9            16    75%
//	10-24          10    60%
//	25+             8    13%
//
// Only 4 of those 122 runs failed at all. The 25+ bucket ends as `partial`, and
// for two distinct reasons, neither of which is an error:
//
//  1. The model stopped mid-thought. One run spent 693 seconds on a SINGLE
//     model call, composing an SVG inside its own reasoning — 82,903 characters
//     ending mid-tag — hit the provider's output ceiling, and returned. It made
//     zero tool calls and wrote no file. The run was recorded as done.
//
//  2. The model abandoned its own plan. Four runs made 65-104 tool calls, never
//     came close to the budget, then declared themselves finished with 1-6
//     items of their own published checklist still undone.
//
// Both were already visible and neither was acted on: finish_reason was carried
// into the eino adapter and read nowhere, and planTracker.unfinished() was read
// once, at finalize, only to relabel the run.
//
// A run should not end while there is a stated reason for it to go on. This is
// the same mechanism steering uses — append and re-enter — because it is the
// same situation: something was said to the model after it had stopped.

// continuationRounds bounds the re-entries. The budget already bounds the total
// work; this bounds how many times we are willing to disagree with the model
// about whether it is done, so a model that will not finish its checklist
// cannot hold the run open indefinitely.
const continuationRounds = 3

// turnTracker records the shape of the run's model turns. It implements
// llm.TurnSink and is installed on the run context.
type turnTracker struct {
	mu    sync.Mutex
	last  llm.Turn
	turns int
	cut   int // turns the provider truncated
}

func newTurnTracker() *turnTracker { return &turnTracker{} }

func (t *turnTracker) ObserveTurn(turn llm.Turn) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns++
	if turn.Truncated() {
		t.cut++
	}
	// Only the orchestrator's turn can be the one the run ended on. A delegate's
	// truncated answer is the delegate's problem, and its caller sees it as a
	// short tool result.
	if turn.Agent == "" || turn.Agent == einoOrchestratorName {
		t.last = turn
	}
}

// lastTurn returns the last orchestrator turn observed.
func (t *turnTracker) lastTurn() llm.Turn {
	if t == nil {
		return llm.Turn{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// truncatedTurns reports how many turns the provider cut short, for the record.
func (t *turnTracker) truncatedTurns() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cut
}

type turnTrackerKey struct{}

func withTurnTracker(ctx context.Context, t *turnTracker) context.Context {
	if t == nil {
		return ctx
	}
	// Installed twice on purpose: the llm package needs it as a sink to write
	// to, the service package needs it as a tracker to read back.
	return llm.WithTurnSink(context.WithValue(ctx, turnTrackerKey{}, t), t)
}

func turnTrackerFrom(ctx context.Context) *turnTracker {
	t, _ := ctx.Value(turnTrackerKey{}).(*turnTracker)
	return t
}

// The instructions handed back to the model. Both are deliberately blunt about
// what went wrong, because the failure in each case is that the model believed
// it was finished.
const (
	truncatedNudge = "[系统] 你上一条回复因为超出单次输出上限被截断,而且没有发起任何工具调用。" +
		"不要在回复里长篇推演或整段起草文件内容——那会再次被截断,而且不会产生任何结果。" +
		"直接调用工具开始做:需要写文件就调用 file_write,需要执行就调用 shell 或 python。"
	unfinishedNudgePrefix = "[系统] 你已经结束了,但你自己列出的计划还有以下步骤没有完成:\n"
	unfinishedNudgeSuffix = "\n请继续完成它们。如果某一项确实做不了或不再需要,明确说明原因,不要略过。"
)

// continuationNudge reports why the run should keep going, as an instruction to
// append to the history — or "" when the run is genuinely finished.
//
// Order matters: a truncated turn is a mechanical failure to speak and comes
// first, since the plan cannot be trusted to be up to date when the turn that
// would have updated it was cut off.
func continuationNudge(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	// A run that spent its allowance has a real reason to stop. Pushing it back
	// to work would spend budget it does not have, and the record already says
	// so.
	if budgetFrom(ctx).exhausted() != "" {
		return ""
	}
	if last := turnTrackerFrom(ctx).lastTurn(); last.Truncated() && last.ToolCalls == 0 {
		return truncatedNudge
	}
	if left := planTrackerFrom(ctx).unfinished(); len(left) > 0 {
		var sb strings.Builder
		sb.WriteString(unfinishedNudgePrefix)
		for _, step := range left {
			sb.WriteString("- ")
			sb.WriteString(step)
			sb.WriteString("\n")
		}
		sb.WriteString(unfinishedNudgeSuffix)
		return sb.String()
	}
	return ""
}

// logContinuation records that the run was pushed back to work. The nudge is
// not shown to the user — they asked for the task, not for a transcript of us
// arguing with the model about whether it is done — so this is the only place
// the decision is visible.
func logContinuation(round int, reason string) {
	kind := "unfinished-plan"
	if strings.HasPrefix(reason, truncatedNudge[:12]) {
		kind = "truncated-turn"
	}
	slog.Info("run continued", "round", round+1, "reason", kind)
}

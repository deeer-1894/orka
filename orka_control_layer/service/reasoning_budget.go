package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/llm"
)

// reasoning_budget.go — stopping a model that is doing the job in its head.
//
// Watched live, with the reasoning streamed out as it was produced, the long
// first turn is not a model that is stuck. It is a model that has decided what
// to do and will not stop deciding. On the pelican-animation task it settled
// its approach in the first few hundred characters — "write an SVG/HTML file…
// per instructions I should call update_plan first" — and then went on to
// design the scene, choose a viewBox, weigh three verification strategies and
// name the output file, all in reasoning: 41,000 characters at six minutes and
// still climbing, with not one character of output and not one tool call. It
// quoted the instruction to get a working version on disk while not doing so.
//
// Nothing upstream bounds this. Reasoning control is a provider field the
// provider may ignore (this endpoint ignores `reasoning_effort` outright, and
// `thinking: disabled` did not stop the above), a prompt instruction is read and
// then planned around, and max_tokens only truncates the answer after the
// thinking is paid for. So the bound is enforced here, on our side of the
// stream.
//
// eino's place for it is WrapModel, whose documented uses include processing
// the response stream. The middleware reads the stream, and when a turn has
// reasoned past its budget without producing any content or tool call, it
// cancels the provider call — a real cancel, so the provider stops generating
// and stops billing — and ends the turn. The opening of the reasoning is kept,
// and the run's continuation hands it back with an instruction to take a SMALL
// first step (see reasoningBudgetNudgePrefix for why "act on your plan" was not
// enough).
//
// It stands down when the user switched deep thinking ON: a hard proof may
// legitimately need a long think with nothing to call.

// reasoningBudgetNudgePrefix introduces the plan handed back.
//
// Its first version said "your plan is enough — act on it", and on the task it
// was built for that did not work. Watched live, the continuation turn read the
// instruction, repeated it back — "the system reminder says I've planned enough
// and should now act: call update_plan first, then write a working version
// with file_write" — and then began designing the whole scene again, from
// "viewBox: 0 0 1200 675", until the budget stopped it a second time. Twice.
//
// Its own reasoning says why. The action it had chosen was to write the ENTIRE
// file, and a tool call's arguments come after the reasoning — so to act at
// all, it believed it first had to design everything. "Act now" was heard as
// "start preparing to act". And the plan handed back was not in fact complete:
// the budget had cut the design mid-bicycle.
//
// So the instruction is not "you have planned enough" but "make the step
// small": a minimal runnable skeleton first, then one part at a time. A small
// next action needs little design, which is the only thing that shortens the
// think in front of it. On the same task that turned a 97-second, 10,000-rune
// think with no output into a 17-second turn with 73 reasoning tokens.
//
// Its second version said to write the skeleton "and end this turn". The model
// did exactly that — and for an agent a reply with no tool call IS the end of
// the run, so it delivered a 47-line skeleton with no animation, promised the
// waves "next turn", and the run was filed as done. There is no next turn after
// a final answer. Now the parts go into update_plan first: if the model stops
// early again, the unfinished-plan continuation that already exists pushes it
// back, instead of this nudge having to hope it does not.
const reasoningBudgetNudgePrefix = "[系统] 你已经想了很久,但还没有任何产出。问题不在于规划不够,而在于你打算一次性写出完整结果——" +
	"那就必须先在脑子里把全部内容设计完,所以你停不下来。换一种做法:把产出拆成很小的步骤,边写边做。\n" +
	"1. 在同一次回复里同时调用 update_plan 和 file_write:update_plan 把要做的部分逐项列出来(每项是一个能单独写进文件的部分),\n" +
	"   file_write 写出一个最小的、能运行的骨架(不超过 60 行),不要在这一步设计其他部分。\n" +
	"2. 然后一项一项往文件里加(用 file_write mode=edit 或 append),每加完一项连同 update_plan 一起发出,需要时运行检查。\n" +
	"用户要的是完整结果:在清单全部完成之前,不要给出最终回复。\n\n" +
	"你之前的思路(只作参考,不必先想完):\n"

// maxPlanCarried caps how much of the reasoning is handed back. Short on
// purpose: the full design notes invited the model to re-read and re-derive
// them before doing anything, which is the loop this is meant to break. The
// opening of a think is where the approach is settled; the rest is detail that
// belongs in the file, written a part at a time.
const maxPlanCarried = 4_000

type reasoningBudget struct {
	*adk.BaseChatModelAgentMiddleware
	runes int    // budget in runes; <= 0 disables
	agent string // whose turns these are, for the turn tracker
}

// newReasoningBudget returns the middleware, or nil when the budget is off —
// appending a nil middleware is what callers must avoid.
func newReasoningBudget(runes int, agent string) adk.ChatModelAgentMiddleware {
	if runes <= 0 {
		return nil
	}
	return &reasoningBudget{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, runes: runes, agent: agent}
}

func (b *reasoningBudget) WrapModel(_ context.Context, m model.BaseChatModel, _ *adk.ModelContext) (model.BaseChatModel, error) {
	return &budgetedModel{inner: m, budget: b}, nil
}

type budgetedModel struct {
	inner  model.BaseChatModel
	budget *reasoningBudget
}

// Generate is not bounded: a non-streamed call returns all at once, so there is
// nothing to watch until it is already paid for. Every agent turn here streams.
func (m *budgetedModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, in, opts...)
}

func (m *budgetedModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if llm.ThinkingIntent(ctx) == llm.ThinkingOn {
		return m.inner.Stream(ctx, in, opts...)
	}
	callCtx, cancel := context.WithCancelCause(ctx)
	src, err := m.inner.Stream(callCtx, in, opts...)
	if err != nil {
		cancel(nil)
		return nil, err
	}
	out, w := schema.Pipe[*schema.Message](8)
	go func() {
		defer w.Close()
		defer src.Close()
		defer cancel(nil)
		var plan strings.Builder
		spent, acted := 0, false
		for {
			msg, err := src.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				w.Send(nil, err)
				return
			}
			if msg != nil {
				if msg.Content != "" || len(msg.ToolCalls) > 0 {
					acted = true
				}
				if !acted && msg.ReasoningContent != "" {
					spent += utf8.RuneCountInString(msg.ReasoningContent)
					if plan.Len() < maxPlanCarried {
						plan.WriteString(msg.ReasoningContent)
					}
				}
			}
			if closed := w.Send(msg, nil); closed {
				return
			}
			if acted || spent < m.budget.runes {
				continue
			}
			// Over budget with nothing to show: stop the provider, end the turn,
			// keep the plan.
			cancel(llm.ErrReasoningBudget)
			llm.ReportTurn(ctx, llm.Turn{
				Agent:          m.budget.agent,
				FinishReason:   llm.FinishReasoningBudget,
				ReasoningRunes: spent,
				Reasoning:      plan.String(),
			})
			slog.Info("reasoning budget reached", "agent", m.budget.agent, "reasoning_runes", spent, "budget", m.budget.runes)
			w.Send(&schema.Message{
				Role:         schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{FinishReason: llm.FinishReasoningBudget},
			}, nil)
			return
		}
	}()
	return out, nil
}

// reasoningBudgetNudge builds the continuation for a turn the budget stopped.
func reasoningBudgetNudge(plan string) string {
	plan = strings.TrimSpace(plan)
	if r := []rune(plan); len(r) > maxPlanCarried {
		plan = string(r[:maxPlanCarried])
	}
	return reasoningBudgetNudgePrefix + plan
}

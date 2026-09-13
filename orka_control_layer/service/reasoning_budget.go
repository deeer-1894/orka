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
// and stops billing — and ends the turn. The reasoning is kept as the plan it
// is, and the run's continuation hands it back with one instruction: the plan
// is made, act on it.
//
// It stands down when the user switched deep thinking ON: a hard proof may
// legitimately need a long think with nothing to call.

// reasoningBudgetNudgePrefix introduces the plan handed back. Blunt for the same
// reason as the other continuation nudges: the failure is that the model
// believed it still had planning to do.
const reasoningBudgetNudgePrefix = "[系统] 你已经规划了很久,但还没有产出任何内容、也没有调用任何工具。" +
	"你下面的规划已经足够了——不要重新规划,不要在回复里起草文件内容。" +
	"现在直接调用第一个工具开始做:复杂任务先调 update_plan,然后立刻用 file_write 写出第一个能运行的版本,再运行、查看、改进。\n\n" +
	"你到目前为止的规划:\n"

// maxPlanCarried caps how much of the reasoning is handed back. The budget
// already bounds it; this only guards against a single oversized final chunk.
const maxPlanCarried = 40_000

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

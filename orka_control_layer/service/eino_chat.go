package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
)

// einoOrchestratorName is the orchestrator agent's name; sub-agent events carry
// their own AgentName, which we map to meta.agent_id for UI lane grouping.
const einoOrchestratorName = "orka"

// This file is the Phase-1 foundation of the Eino-library migration: it stands
// up a real eino adk.ChatModelAgent + Runner backed by our existing llm.Client
// (via llm.EinoModel) and tool suite (via EinoTools), running in parallel to the
// hand-rolled runner and gated by config so the working path is untouched.

// budgetGuard enforces a run's budget on every model cycle. When the budget is
// spent it strips the tools — so the model MUST answer from what it already has
// instead of erroring out at eino's hard MaxIterations cliff — and appends a
// notice telling it to report honestly rather than invent a conclusion.
// Model-agnostic: it removes the *ability* to call tools rather than asking.
//
// The budget is shared with the caller (via the run context), so whoever
// finalizes the run can see that it stopped early and file it as partial.
// Sub-agents get their own budget scoped to their own step allowance.
type budgetGuard struct {
	*adk.BaseChatModelAgentMiddleware
	budget *runBudget
}

func newBudgetGuard(maxIters int) *budgetGuard {
	return newBudgetGuardFor(newRunBudget(maxIters, 0, 0))
}

func newBudgetGuardFor(b *runBudget) *budgetGuard {
	return &budgetGuard{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, budget: b}
}

func (g *budgetGuard) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if !g.budget.observe(state.Messages) {
		return ctx, state, nil
	}
	// Budget spent: last allowed call → no tools, and say so.
	state.ToolInfos = nil
	state.DeferredToolInfos = nil
	state.Messages = append(state.Messages, budgetNotice(g.budget.exhausted()))
	return ctx, state, nil
}

// summarizationHandlers returns the eino summarization middleware so long runs
// fold older context into a running summary instead of overflowing — the native
// replacement for the hand-rolled Memory truncation (which dropped old turns).
// Triggers on message count to avoid a token-counter dependency; the agent's own
// model generates the summary.
// summarizationTrigger sets BOTH thresholds, and leaving either at zero is a
// trap rather than a default.
//
// eino's shouldSummarize checks the message count, then FALLS THROUGH to a token
// check — and getTriggerContextTokens only substitutes its own 160k default when
// the whole Trigger is nil. A Trigger carrying just ContextMessages leaves
// ContextTokens at 0, so `tokens > 0` matches every call and the last resort
// becomes unconditional.
//
// It did, silently. The summarizer ran on all 23 orchestrator cycles of a run
// whose message count never exceeded 8, at 38-41s per call: 876 seconds, 60% of
// that run's model time and three times the orchestrator's own, spent
// compressing a context that was never large. It makes no tool call and never
// reaches the run record, so nothing pointed at it until the model calls were
// timed per agent.
//
// The token threshold sits well above clearAboveTokens on purpose: reduction is
// the cheap layer and must get its chance first, or a model call is paying to do
// a placeholder's job.
func summarizationTrigger() *summarization.TriggerCondition {
	return &summarization.TriggerCondition{ContextMessages: 48, ContextTokens: 120_000}
}

func summarizationHandlers(ctx context.Context, client llm.Client, model string) []adk.ChatModelAgentMiddleware {
	mw, err := summarization.New(ctx, &summarization.Config{
		Model: llm.NewEinoModel(client, model).ForAgent("summarizer"),
		// Reduction now trims oversized/stale tool output first, so summarization
		// is the backstop for genuinely long dialogue — trigger it earlier than the
		// old 80-message mark, which a long pipeline blew past on cost alone.
		//
		Trigger: summarizationTrigger(),
	})
	if err != nil {
		return nil // summarization is best-effort; never block agent construction
	}
	return []adk.ChatModelAgentMiddleware{bestEffort(mw)}
}

// bestEffortMiddleware makes a context-management middleware unable to fail a
// run. Its job is to keep the context small; if it cannot, the right outcome is
// a larger context, not a dead run.
//
// It was not: eino treats a middleware error as a NodeRunError and aborts. The
// summarizer's own model call can return empty content ("summary content is
// empty") or simply fail, and 11 runs here died that way — including one that
// had already implemented two N-Queens solvers, verified all five counts against
// known values and benchmarked them, losing the lot to a failed summary of work
// that was already finished.
type bestEffortMiddleware struct {
	adk.ChatModelAgentMiddleware
}

func bestEffort(mw adk.ChatModelAgentMiddleware) adk.ChatModelAgentMiddleware {
	return &bestEffortMiddleware{ChatModelAgentMiddleware: mw}
}

func (b *bestEffortMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	newCtx, newState, err := b.ChatModelAgentMiddleware.BeforeModelRewriteState(ctx, state, mc)
	if err != nil {
		// Carry on with the un-summarised state. Worst case the context is
		// bigger than intended, which the token budget already guards against.
		return ctx, state, nil
	}
	return newCtx, newState, nil
}

// BuildEinoAgent constructs an eino ReAct ChatModelAgent over our model + tools.
// backup is an optional other-tier model to fail over to when retries are
// exhausted (nil = no failover). handlers are optional middlewares (e.g.
// summarization) the production path supplies; the minimal/test path passes none
// for deterministic behavior.
func BuildEinoAgent(ctx context.Context, client llm.Client, model, instruction string, tools []agent.BaseTool, maxIters int, backup einomodel.BaseChatModel, handlers ...adk.ChatModelAgentMiddleware) (adk.Agent, error) {
	if maxIters <= 0 {
		maxIters = 16
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "orka",
		Description: "Orka assistant",
		Instruction: instruction,
		Model:       llm.NewEinoModel(client, model).ForAgent("orka"),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: EinoTools(withFindTools(withPlan(withClarify(tools))))},
			ReturnDirectly:  clarifyReturnDirectly(),
		},
		MaxIterations: maxIters,
		// Survive transient/mid-stream model failures instead of failing the run.
		ModelRetryConfig:    modelRetryConfig(),
		ModelFailoverConfig: modelFailoverConfig(backup),
		Handlers:            append([]adk.ChatModelAgentMiddleware{newBudgetGuardFor(agentBudget(ctx, maxIters)), newGateMiddleware(toolGateFrom(ctx))}, handlers...),
	})
}

// BuildEinoSubAgentTools builds each sub-agent spec as a NATIVE eino
// ChatModelAgent (scoped tools, prompt, model) wrapped via adk.NewAgentTool, so
// the orchestrator delegates to them as real eino agents (Phase 3) rather than
// through the hand-rolled runner. Mirrors BuildSubAgents' scoping/model rules.
func BuildEinoSubAgentTools(ctx context.Context, mainClient llm.Client, mainModel string, miniClient llm.Client, miniModel string, atomic []agent.BaseTool, specs []config.SubAgentConfig) ([]tool.BaseTool, error) {
	if len(specs) == 0 {
		specs = DefaultSubAgents()
	}
	byName := map[string]agent.BaseTool{}
	for _, t := range atomic {
		byName[t.Name()] = t
	}
	var out []tool.BaseTool
	for _, sp := range specs {
		if sp.Name == "" {
			continue
		}
		var scoped []agent.BaseTool
		for _, n := range sp.Tools {
			if t, ok := byName[n]; ok {
				scoped = append(scoped, t)
			}
		}
		if len(scoped) == 0 {
			continue // none of this agent's tools available; don't expose a dead agent
		}
		client, model := miniClient, miniModel
		if sp.Model == "main" {
			client, model = mainClient, mainModel
		}
		prompt := sp.Prompt
		if prompt == "" {
			prompt = needInput
		} else {
			prompt += " " + needInput
		}
		iters := sp.MaxIters
		if iters <= 0 {
			iters = 12
		}
		sub, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
			Name:        sp.Name,
			Description: sp.Description,
			Instruction: prompt,
			Model:       llm.NewEinoModel(client, model).ForAgent(sp.Name),
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{Tools: EinoTools(scoped)},
			},
			MaxIterations: iters,
			// Same resilience as the orchestrator: a delegated worker that dies on a
			// transient blip still fails the parent step.
			ModelRetryConfig:    modelRetryConfig(),
			ModelFailoverConfig: modelFailoverConfig(backupModel(mainClient, mainModel, model)),
			// A token budget as well as a step budget. MaxIterations bounds model
			// CYCLES, and once the prompt encourages batching one cycle can emit
			// several tool calls — so a researcher told to use "~6, at most ~10"
			// calls was measured making 15. A soft instruction does not bound a
			// delegate; four of them over-researching is how a run reaches 399k
			// tokens and still ends unfinished.
			// The budget bounds what a delegate may SPEND; the context handlers
			// bound what it carries while spending it. Without the latter a
			// delegate's own retrieval sat verbatim until the budget cut it off
			// mid-work, which is the expensive way to learn a context is too big.
			Handlers: append([]adk.ChatModelAgentMiddleware{
				newBudgetGuardFor(newRunBudget(iters, subAgentMaxTokens, 0)),
			}, subAgentContextHandlers(ctx, sp.Name, scoped)...),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, adk.NewAgentTool(ctx, sub))
	}
	return out, nil
}

// BuildEinoOrchestrator builds the orchestrator agent: atomic tools PLUS native
// sub-agent tools, with EmitInternalEvents so sub-agent events stream up to the
// user (tagged by AgentName → meta.agent_id for lane grouping).
// extra middlewares (e.g. the context-management chain) run before the built-in
// summarization backstop.
func BuildEinoOrchestrator(ctx context.Context, mainClient llm.Client, mainModel string, miniClient llm.Client, miniModel, instruction string, atomic []agent.BaseTool, specs []config.SubAgentConfig, maxIters int, summarize bool, extra ...adk.ChatModelAgentMiddleware) (adk.Agent, error) {
	subTools, err := BuildEinoSubAgentTools(ctx, mainClient, mainModel, miniClient, miniModel, atomic, specs)
	if err != nil {
		return nil, err
	}
	if maxIters <= 0 {
		maxIters = 16
	}
	allTools := append(EinoTools(withFindTools(withPlan(withClarify(atomic)))), subTools...)
	handlers := append([]adk.ChatModelAgentMiddleware{newBudgetGuardFor(agentBudget(ctx, maxIters)), newGateMiddleware(toolGateFrom(ctx))}, extra...)
	if summarize {
		// Summarize history compression on the FAST mini model — it's an auxiliary
		// step, not user-facing synthesis, so it doesn't need the strong model.
		handlers = append(handlers, summarizationHandlers(ctx, miniClient, miniModel)...)
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        einoOrchestratorName,
		Description: "Orka orchestrator",
		Instruction: instruction,
		Model:       llm.NewEinoModel(mainClient, mainModel).ForAgent(einoOrchestratorName),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig:    compose.ToolsNodeConfig{Tools: allTools},
			ReturnDirectly:     clarifyReturnDirectly(),
			EmitInternalEvents: true, // stream sub-agent events up for lane rendering
		},
		MaxIterations: maxIters,
		// A long orchestrated run makes many model calls; one transient blip must
		// not end it. Retry mid-stream failures, then fail over to the mini tier.
		ModelRetryConfig:    modelRetryConfig(),
		ModelFailoverConfig: modelFailoverConfig(backupModel(miniClient, miniModel, mainModel)),
		Handlers:            handlers,
	})
}

// subAgentMaxTokens caps ONE delegate's context growth. Generous enough for real
// multi-source research, low enough that a delegate cannot spend the whole run's
// budget on its own subtask.
const subAgentMaxTokens = 80_000

// einoMaxIters is the orchestrator/agent generation-cycle cap for the prod path.
//
// It was 16, which the budget guard turns into 15 usable cycles, and that was
// the ceiling on how complex a task could BE. Measured across 333 runs, the
// completion rate is flat at 67-71% up to 30 tool calls and collapses to 18%
// beyond it — and 30 calls is exactly 15 cycles at the 2.1 calls-per-cycle the
// batching prompt achieves. The runs that did finish real work spent 43 and 94
// calls; under a 15-cycle budget neither could have.
//
// The number was sized for an orchestrator that delegates, spending its cycles
// on fan-out rather than atomic work. Delegation runs at 1.17% of tool calls
// (2.33% after the prompt rewrite in ff0141a), so in practice the orchestrator
// does the atomic work itself and pays a cycle for every couple of tool calls.
// Raising the ceiling is the honest response to what it actually does; the
// prompt has already been tried.
//
// 40 covers ~84 tool calls at the measured batching rate. It is not a licence to
// run forever: runMaxTokens (800k) and runMaxWall (2h) still bound the run, and
// they bound it by COST rather than by how many steps the task happens to need,
// which is the right shape of limit.
const einoMaxIters = 40

// RunEinoOnce runs the agent to completion on a single user message and returns
// the final assistant text. Tool steps and intermediate assistant turns are
// drained; only the last assistant message content is returned. (Phase 2 will
// fan these events out to SSE instead of collapsing them.)
func RunEinoOnce(ctx context.Context, ag adk.Agent, userMessage string) (string, error) {
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag})
	iter := runner.Query(ctx, userMessage)
	var final string
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return "", ev.Err
		}
		out := ev.Output
		if out == nil || out.MessageOutput == nil || out.MessageOutput.IsStreaming {
			continue
		}
		if m := out.MessageOutput.Message; m != nil && m.Role == schema.Assistant && m.Content != "" {
			final = m.Content
		}
	}
	return final, nil
}

// toEinoMessages converts our chat history into eino schema messages (user /
// assistant turns only; the system prompt is supplied via the agent's
// Instruction, and tool/system events are not replayed into the model input).
func toEinoMessages(msgs []messages.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Type != messages.EventChat {
			continue
		}
		switch m.Role {
		case messages.RoleUser:
			out = append(out, schema.UserMessage(m.Content))
		case messages.RoleAssistant:
			out = append(out, schema.AssistantMessage(m.Content, nil))
		}
	}
	return out
}

// StreamEinoRun executes the chat on the eino runtime, fanning each Runner event
// into the SAME emit/persist sink (rc.Emit → s.Msg.Deliver) the hand-rolled path
// uses, so persistence and SSE rendering are identical: live EventStream token
// deltas during generation, then the authoritative (persisted) EventChat; tool
// calls correlated with their results into a {tool,args,result} receipt.
func StreamEinoRun(ctx context.Context, rc *agent.RunContext, ag adk.Agent, emit func(messages.Message)) error {
	// Surface the model's live "thinking" tokens (reasoning_content) as a side
	// channel so long reasoning calls show visible progress, not a stall.
	ctx = llm.WithReasoningSink(ctx, func(delta string) {
		emit(messages.ReasoningDelta(delta, rc.Meta))
	})
	// With a checkpoint store the Runner can persist an interrupted run and
	// resume it later — that is what lets a danger-tool confirmation pause the
	// run instead of parking a goroutine (and survive a restart).
	store := checkpointStoreFrom(ctx)
	ckptID := rc.Meta.ConversationID
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag, EnableStreaming: true, CheckPointStore: store})

	// The model input is either a fresh conversation or the transcript of an
	// earlier attempt that died. Resuming replays what was already established
	// instead of paying for it again.
	input := toEinoMessages(rc.Messages)
	if rr := runResumeFrom(ctx); rr != nil {
		input = rr.Messages
	}
	journal := journalFrom(ctx)
	journal.setSeed(input)
	// Capture the tail of the transcript even when a run ends without a trailing
	// tool call — a run that dies on the final synthesis has still done the work.
	defer journal.flush()

	var iter *adk.AsyncIterator[*adk.AgentEvent]
	if rs := resumeFrom(ctx); rs != nil && store != nil && ckptID != "" {
		var err error
		iter, err = runner.ResumeWithParams(ctx, ckptID,
			&adk.ResumeParams{Targets: map[string]any{rs.Target: rs.Data}})
		if err != nil {
			return err
		}
	} else if store != nil && ckptID != "" {
		iter = runner.Run(ctx, input, adk.WithCheckPointID(ckptID))
	} else {
		iter = runner.Run(ctx, input)
	}

	type pendingCall struct {
		name string
		args map[string]any
	}
	calls := map[string]pendingCall{} // tool_call_id → call info (for args)
	tokens, toolCalls := 0, 0         // per-run audit counters (incl. sub-agents)
	lastFinal := ""                   // orchestrator's final answer → run output
	defer func() {
		rc.Put(middlewares.VarRunTokens, tokens)
		rc.Put(middlewares.VarRunTools, toolCalls)
		if lastFinal != "" {
			middlewares.SetFinal(rc, lastFinal)
		}
	}()

	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		// A danger tool asked for approval: the Runner has checkpointed the run,
		// so surface the request and RETURN. Nothing is blocked — /chat/confirm
		// resumes from the checkpoint, even after a control-plane restart.
		if ev.Action != nil && ev.Action.Interrupted != nil {
			if ci, target, okc := confirmFromInterrupt(ev.Action.Interrupted); okc {
				rc.Interrupt = &agent.Interrupt{Reason: "confirm"}
				rc.Put(varPendingConfirm, pausedRun{Target: target, Tool: ci.Tool, Summary: ci.Summary})
				emit(messages.Confirm(messages.ConfirmRequest{
					ID: target, Tool: ci.Tool, Summary: ci.Summary,
				}, rc.Meta))
				return nil
			}
		}
		if ev.Err != nil {
			// Graceful degradation: hitting the iteration cap shouldn't hard-fail
			// a long, expensive run — surface a note and return what we have so the
			// run is recorded as done-with-partial rather than failed.
			if errors.Is(ev.Err, adk.ErrExceedMaxIterations) {
				if lastFinal == "" {
					note := "(已达到本轮迭代上限,基于已获取的信息作答;如需更深入可继续追问。)"
					emit(messages.Chat(messages.RoleAssistant, note, rc.Meta))
					lastFinal = note
				}
				return nil
			}
			return ev.Err
		}
		out := ev.Output
		if out == nil || out.MessageOutput == nil {
			continue
		}
		mv := out.MessageOutput

		// Sub-agent events carry their own AgentName; map it to meta.agent_id so
		// the UI groups them into a lane (the orchestrator's own events keep "").
		eventMeta := rc.Meta
		if ev.AgentName != "" && ev.AgentName != einoOrchestratorName {
			eventMeta.AgentID = ev.AgentName
		}

		var m *schema.Message
		if mv.IsStreaming {
			full, err := drainEinoStream(mv.MessageStream, mv.Role, eventMeta, emit)
			if err != nil {
				return err
			}
			m = full
		} else {
			m = mv.Message
		}
		if m == nil {
			continue
		}

		if m.ResponseMeta != nil && m.ResponseMeta.Usage != nil {
			tokens += m.ResponseMeta.Usage.TotalTokens
		}

		// Journal every authoritative message. This is the run's transcript, and
		// the only thing that makes a mid-run failure recoverable rather than
		// total. Sub-agent turns are journaled too: they are part of what the
		// orchestrator has already established.
		journal.append(m)

		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				calls[tc.ID] = pendingCall{name: tc.Function.Name, args: parseJSONArgs(tc.Function.Arguments)}
			}
			if m.Content != "" {
				if eventMeta.AgentID == "" {
					rc.Messages = append(rc.Messages, messages.Chat(messages.RoleAssistant, m.Content, eventMeta))
					lastFinal = m.Content // orchestrator's answer → run output summary
				}
				emit(messages.Chat(messages.RoleAssistant, m.Content, eventMeta))
			}
		case schema.Tool:
			pc := calls[m.ToolCallID]
			name := pc.name
			if name == "" {
				name = m.ToolName
			}
			// clarify pauses the run: surface the question and interrupt so the
			// existing checkpoint/resume machinery takes over (the clarify question
			// is recorded into history so the resumed run has full context).
			if name == middlewares.ClarifyToolName {
				clar := clarifyFromArgs(pc.args)
				rc.Messages = append(rc.Messages, messages.Chat(messages.RoleAssistant, "❓ "+clar.Question, rc.Meta))
				rc.Interrupt = &agent.Interrupt{Reason: "clarify", Clarify: &clar}
				return nil
			}
			toolCalls++
			payload := map[string]any{"tool": name, "args": pc.args, "result": m.Content}
			emit(messages.Tool("call", payload, eventMeta))
			// Durability boundary: the work this result describes has already
			// happened, so persist the transcript now. Tens of writes per run —
			// nothing next to the model call that produced it.
			journal.flush()
		}
	}
	return nil
}

// drainEinoStream reads an assistant generation stream, emitting each content
// delta as a live (non-persisted) EventStream frame, and returns the merged
// final message (content + tool calls) for the authoritative persist step.
func drainEinoStream(s *schema.StreamReader[*schema.Message], role schema.RoleType, meta messages.Meta, emit func(messages.Message)) (*schema.Message, error) {
	defer s.Close()
	var chunks []*schema.Message
	var content strings.Builder
	for {
		chunk, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk == nil {
			continue
		}
		if role == schema.Assistant && chunk.Content != "" {
			content.WriteString(chunk.Content)
			emit(messages.StreamDelta(chunk.Content, meta))
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	// ConcatMessages merges deltas + tool-call fragments; on any hiccup fall back
	// to a message carrying just the accumulated text.
	if full, err := schema.ConcatMessages(chunks); err == nil {
		return full, nil
	}
	return &schema.Message{Role: role, Content: content.String()}, nil
}

// runEino is the production entry: it builds an eino agent over the request's
// model + tools + system prompt and streams its events into rc's emit sink.
func (s *ChatService) runEino(ctx context.Context, rc *agent.RunContext, deps PipelineDeps, tools []agent.BaseTool, client llm.Client, model string, _ func(messages.Message)) error {
	instruction := deps.SystemPrompt
	var ag adk.Agent
	var err error
	// Build with rc.Ctx, not the bare run ctx. The run's budget, tool gate and
	// plan tracker are installed on rc.Ctx, and middlewares read them at
	// CONSTRUCTION time — built from the bare ctx they silently got nothing, so
	// the tool gate never narrowed anything and the budget guard enforced a
	// private budget that finalizeRun could not see (making a budget-exhausted
	// run still file as "done"). rc.Ctx derives from ctx, so it is a superset.
	if rc.Ctx != nil {
		ctx = rc.Ctx
	}
	// Sub-agents are built inside BuildEinoOrchestrator, past the last point that
	// can see storage config; this is how they reach an offload backend of their
	// own (see subAgentContextHandlers).
	ctx = withOffloadRoot(ctx, s.Cfg.Storage.BaseStoragePath)
	// Context-window management (truncate oversized tool output to a workspace
	// file, clear stale tool results, repair dangling tool calls). Runs ahead of
	// the summarization backstop.
	ctxMW := contextHandlers(ctx, s.Cfg.Storage.BaseStoragePath, runUserEmail(rc), einoOrchestratorName, tools, s.Cfg.Agent.SubAgents)
	// Automatic tier selection, when asked for. Prepended so it decides before
	// the other middlewares see the call.
	if r := s.routerFor(rc, model); r != nil {
		ctxMW = append([]adk.ChatModelAgentMiddleware{r}, ctxMW...)
		rc.Put(varModelRouter, r)
	}
	if s.Cfg.Agent.MultiAgent {
		if instruction == "" {
			instruction = OrchestratorPrompt
		}
		miniModel := s.Cfg.LLM.MiniModel
		if miniModel == "" {
			miniModel = model
		}
		// einoMaxIters, not a literal: this is eino's own hard cycle cliff, and the
		// run budget is built from the same constant. Drifting apart means either
		// the guard never fires (and eino errors out instead of reporting) or it
		// fires far too early.
		ag, err = BuildEinoOrchestrator(ctx, client, model, s.Mini, miniModel, instruction, tools, s.Cfg.Agent.SubAgents, einoMaxIters, !s.DisableSummary, ctxMW...)
	} else {
		if instruction == "" {
			instruction = middlewares.DefaultSystemPrompt
		}
		var sum []adk.ChatModelAgentMiddleware
		if !s.DisableSummary {
			// Auxiliary history compression runs on the fast mini model.
			sumClient, sumModel := s.Mini, s.Cfg.LLM.MiniModel
			if sumClient == nil {
				sumClient, sumModel = client, model
			}
			if sumModel == "" {
				sumModel = model
			}
			sum = summarizationHandlers(ctx, sumClient, sumModel)
		}
		if len(ctxMW) > 0 {
			sum = append(append([]adk.ChatModelAgentMiddleware{}, ctxMW...), sum...)
		}
		// Fail over to the other model tier when the primary exhausts its retries.
		ag, err = BuildEinoAgent(ctx, client, model, instruction, tools, einoMaxIters,
			backupModel(s.Mini, s.Cfg.LLM.MiniModel, model), sum...)
	}
	if err != nil {
		return err
	}
	// Run with rc.Ctx (carries the emit sink + run meta), so tools invoked by the
	// eino runner — e.g. the confirmation gate — can stream events into the SSE.
	runCtx := ctx
	if rc.Ctx != nil {
		runCtx = rc.Ctx
	}
	return StreamEinoRun(runCtx, rc, ag, rc.Emit)
}

func parseJSONArgs(s string) map[string]any {
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return map[string]any{"_raw": s}
	}
	return m
}

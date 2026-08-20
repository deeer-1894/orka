package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
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

// budgetGuard forces an agent to wrap up before the hard MaxIterations cliff:
// on the last allowed model call it strips the tools, so the model MUST answer
// from what it already has instead of erroring out at the cap (the cause of
// expensive deep-research runs dying with no result). Model-agnostic — it
// removes the *ability* to call tools rather than politely asking.
type budgetGuard struct {
	*adk.BaseChatModelAgentMiddleware
	maxIters int
}

func newBudgetGuard(maxIters int) *budgetGuard {
	return &budgetGuard{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, maxIters: maxIters}
}

func (g *budgetGuard) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if g.maxIters > 1 {
		// assistant messages == model-generation cycles so far.
		n := 0
		for _, m := range state.Messages {
			if m != nil && m.Role == schema.Assistant {
				n++
			}
		}
		if n >= g.maxIters-1 { // last allowed call → no tools, force synthesis
			state.ToolInfos = nil
			state.DeferredToolInfos = nil
		}
	}
	return ctx, state, nil
}

const emptyModelRetryPrompt = "The previous model response contained only internal reasoning or no usable output. Respond now with either a valid tool call or a concise final answer. Do not output <think> tags or reasoning alone."

const einoRejectedOutputPrefix = "model output rejected by ShouldRetry at attempt "

// modelResponseRetryConfig recovers from a response shape seen with local
// reasoning models: HTTP 200 followed by reasoning_content, but no final text
// or tool call. Retrying with an explicit correction gives the model a new turn
// boundary instead of failing later with an opaque empty-result error.
func modelResponseRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: 2,
		ShouldRetry: func(_ context.Context, retryCtx *adk.RetryContext) *adk.RetryDecision {
			if retryCtx.Err != nil || usableAssistantOutput(retryCtx.OutputMessage) || governedSalesBIContinuation(retryCtx.InputMessages) {
				return &adk.RetryDecision{Retry: false}
			}
			modified := append([]*schema.Message(nil), retryCtx.InputMessages...)
			modified = append(modified, schema.UserMessage(emptyModelRetryPrompt))
			return &adk.RetryDecision{
				Retry:                 true,
				ModifiedInputMessages: modified,
				RejectReason:          "empty_or_reasoning_only_model_response",
			}
		},
	}
}

func governedSalesBIContinuation(input []*schema.Message) bool {
	for i := len(input) - 1; i >= 0; i-- {
		m := input[i]
		if m == nil {
			continue
		}
		if m.Role == schema.Tool {
			if m.Name == "sales_query_answer" || m.Name == "sales_report_generate" {
				return true
			}
			continue
		}
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				if tc.Function.Name == "sales_query_answer" || tc.Function.Name == "sales_report_generate" {
					return true
				}
			}
		}
		if m.Role == schema.User {
			return false
		}
	}
	return false
}

func usableAssistantOutput(m *schema.Message) bool {
	if m == nil {
		return false
	}
	if len(m.ToolCalls) > 0 {
		return true
	}
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return false
	}
	// OpenAIClient falls reasoning_content back into Content for non-agent
	// consumers. Reject that synthetic shape here so agents do not expose CoT.
	if reasoning := strings.TrimSpace(m.ReasoningContent); reasoning != "" && content == reasoning {
		return false
	}
	return stripThinkSections(content) != ""
}

func stripThinkSections(content string) string {
	for {
		lower := strings.ToLower(content)
		start := strings.Index(lower, "<think>")
		if start < 0 {
			break
		}
		bodyStart := start + len("<think>")
		endRel := strings.Index(lower[bodyStart:], "</think>")
		if endRel < 0 {
			content = content[:start]
			break
		}
		end := bodyStart + endRel + len("</think>")
		content = content[:start] + content[end:]
	}
	return strings.TrimSpace(strings.ReplaceAll(content, "</think>", ""))
}

func isEinoRetryNotice(err error) bool {
	if err == nil {
		return false
	}
	var retrying *adk.WillRetryError
	return errors.As(err, &retrying) || strings.HasPrefix(err.Error(), einoRejectedOutputPrefix)
}

// summarizationHandlers returns the eino summarization middleware so long runs
// fold older context into a running summary instead of overflowing — the native
// replacement for the hand-rolled Memory truncation (which dropped old turns).
// Triggers on message count to avoid a token-counter dependency; the agent's own
// model generates the summary.
func summarizationHandlers(ctx context.Context, client llm.Client, model string) []adk.ChatModelAgentMiddleware {
	mw, err := summarization.New(ctx, &summarization.Config{
		Model:   llm.NewEinoModel(client, model),
		Trigger: &summarization.TriggerCondition{ContextMessages: 80},
	})
	if err != nil {
		return nil // summarization is best-effort; never block agent construction
	}
	return []adk.ChatModelAgentMiddleware{mw}
}

// BuildEinoAgent constructs an eino ReAct ChatModelAgent over our model + tools.
// handlers are optional middlewares (e.g. summarization) the production path
// supplies; the minimal/test path passes none for deterministic behavior.
func BuildEinoAgent(ctx context.Context, client llm.Client, model, instruction string, tools []agent.BaseTool, maxIters int, handlers ...adk.ChatModelAgentMiddleware) (adk.Agent, error) {
	if maxIters <= 0 {
		maxIters = 16
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:             "orka",
		Description:      "Orka assistant",
		Instruction:      instruction,
		ModelRetryConfig: modelResponseRetryConfig(),
		Model:            llm.NewEinoModel(client, model),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: EinoTools(withPlan(withClarify(tools)))},
			ReturnDirectly:  clarifyReturnDirectly(),
		},
		MaxIterations: maxIters,
		Handlers:      append([]adk.ChatModelAgentMiddleware{newBudgetGuard(maxIters)}, handlers...),
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
	if err := validateSubAgentTools(specs); err != nil {
		return nil, err
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
			Name:             sp.Name,
			Description:      sp.Description,
			Instruction:      prompt,
			ModelRetryConfig: modelResponseRetryConfig(),
			Model:            llm.NewEinoModel(client, model),
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{Tools: EinoTools(scoped)},
			},
			MaxIterations: iters,
			Handlers:      []adk.ChatModelAgentMiddleware{newBudgetGuard(iters)},
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
func BuildEinoOrchestrator(ctx context.Context, mainClient llm.Client, mainModel string, miniClient llm.Client, miniModel, instruction string, atomic []agent.BaseTool, specs []config.SubAgentConfig, maxIters int, summarize bool) (adk.Agent, error) {
	subTools, err := BuildEinoSubAgentTools(ctx, mainClient, mainModel, miniClient, miniModel, atomic, specs)
	if err != nil {
		return nil, err
	}
	if maxIters <= 0 {
		maxIters = 16
	}
	allTools := append(EinoTools(withPlan(withClarify(atomic))), subTools...)
	handlers := []adk.ChatModelAgentMiddleware{newBudgetGuard(maxIters)}
	if summarize {
		// Summarize history compression on the FAST mini model — it's an auxiliary
		// step, not user-facing synthesis, so it doesn't need the strong model.
		handlers = append(handlers, summarizationHandlers(ctx, miniClient, miniModel)...)
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:             einoOrchestratorName,
		Description:      "Orka orchestrator",
		Instruction:      instruction,
		ModelRetryConfig: modelResponseRetryConfig(),
		Model:            llm.NewEinoModel(mainClient, mainModel),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig:    compose.ToolsNodeConfig{Tools: allTools},
			ReturnDirectly:     clarifyReturnDirectly(),
			EmitInternalEvents: true, // stream sub-agent events up for lane rendering
		},
		MaxIterations: maxIters,
		Handlers:      handlers,
	})
}

// einoMaxIters is the orchestrator/agent generation-cycle cap for the prod path.
const einoMaxIters = 16

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
			if isEinoRetryNotice(ev.Err) {
				continue
			}
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
	runnerCtx, cancelRunner := context.WithCancel(ctx)
	defer cancelRunner()
	assistCapture := &salesBIAssistCapture{cancel: cancelRunner}
	runnerCtx = withSalesBIAssistCapture(runnerCtx, assistCapture)
	runner := adk.NewRunner(runnerCtx, adk.RunnerConfig{Agent: ag, EnableStreaming: true})
	iter := runner.Run(runnerCtx, toEinoMessages(rc.Messages))

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
		if captured, ok := assistCapture.take(); ok {
			toolCalls++
			emit(messages.Tool("call", map[string]any{
				"tool":   captured.toolName,
				"args":   salesBIAuditArgs(captured.toolName, captured.args),
				"result": salesBIAuditResult(captured.toolName, captured.raw),
			}, rc.Meta))
			if pauseForSalesBIAssist(rc, captured.raw, rc.Meta) {
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
			if isEinoRetryNotice(ev.Err) {
				continue
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
				if isEinoRetryNotice(err) {
					continue
				}
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
			payload := map[string]any{
				"tool":   name,
				"args":   salesBIAuditArgs(name, pc.args),
				"result": salesBIAuditResult(name, m.Content),
			}
			emit(messages.Tool("call", payload, eventMeta))
			if eventMeta.AgentID != "" {
				continue
			}

			// Publish returns only analysis_text. Combine it with the locked
			// sealed prefix and stop before a third model generation can rewrite it.
			if name == "sales_query_publish" {
				prefix := rc.Str(salesBILockedPrefixKey)
				analysisText := publishedAnalysisText(m.Content)
				if prefix != "" && analysisText != "" {
					part := messages.Chat(messages.RoleAssistant, analysisText, eventMeta)
					rc.Messages = append(rc.Messages, part)
					emit(part)
					lastFinal = prefix + "\n\n" + analysisText
					delete(rc.Vars, salesBIAnalysisPendingKey)
					return nil
				}
			}

			if name == "sales_report_generate" && pendingReportID(m.Content) != "" {
				rc.Put(salesBIReportPendingKey, m.Content)
			}

			v := governedToolResult(name, m.Content)
			switch {
			case v.Terminal:
				if name == "sales_report_generate" {
					delete(rc.Vars, salesBIReportPendingKey)
				}
				answer := messages.Chat(messages.RoleAssistant, v.SealedAnswer, eventMeta)
				rc.Messages = append(rc.Messages, answer)
				lastFinal = v.SealedAnswer
				emit(answer)
				return nil
			case v.Continuation == "analysis_publish":
				// The complete tool result remains in Eino's context, including the
				// fact ledger needed to construct the publish narrative.
				prefix := messages.Chat(messages.RoleAssistant, v.SealedAnswer, eventMeta)
				rc.Messages = append(rc.Messages, prefix)
				rc.Put(salesBILockedPrefixKey, v.SealedAnswer)
				rc.Put(salesBIAnalysisPendingKey, m.Content)
				lastFinal = v.SealedAnswer
				emit(prefix)
			case v.Continuation == "assist":
				raw := m.Content
				if captured, ok := assistCapture.take(); ok {
					raw = captured.raw
				}
				if pauseForSalesBIAssist(rc, raw, eventMeta) {
					return nil
				}
				answer := messages.Chat(messages.RoleAssistant, v.SealedAnswer, eventMeta)
				rc.Messages = append(rc.Messages, answer)
				lastFinal = v.SealedAnswer
				emit(answer)
				return nil
			}
		}
	}
	if lastFinal == "" {
		return errors.New("model completed without assistant content or tool call")
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
	if s.Cfg.Agent.MultiAgent {
		if instruction == "" {
			instruction = OrchestratorPrompt
		}
		miniModel := s.Cfg.LLM.MiniModel
		if miniModel == "" {
			miniModel = model
		}
		ag, err = BuildEinoOrchestrator(ctx, client, model, s.Mini, miniModel, instruction, tools, s.Cfg.Agent.SubAgents, 16, !s.DisableSummary)
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
		ag, err = BuildEinoAgent(ctx, client, model, instruction, tools, einoMaxIters, sum...)
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
	streamErr := StreamEinoRun(runCtx, rc, ag, rc.Emit)
	if rc.Str(salesBIReportPendingKey) != "" {
		if errors.Is(streamErr, context.Canceled) {
			return streamErr
		}
		return publishPendingSalesBIReport(runCtx, rc, tools)
	}
	if streamErr != nil {
		return streamErr
	}
	if rc.Str(salesBIAnalysisPendingKey) != "" {
		return publishPendingSalesBIAnalysis(runCtx, rc, client, model, instruction, tools)
	}
	return nil
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

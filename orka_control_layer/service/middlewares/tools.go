package middlewares

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/trace"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/obs"
)

// Tools runs the ReAct loop: call the model, execute any tool calls, feed the
// results back, and repeat until the model produces a final answer. A call to
// the built-in `clarify` tool sets rc.Interrupt so the runner stops with the
// cursor on this middleware (so resume re-enters the loop with the user reply).
type Tools struct {
	LLM        llm.Client
	Model      string
	MaxIters   int
	MaxHistory int           // bound context across ReAct iterations (0 = unbounded)
	GUIBudget  time.Duration // cumulative wall-time the browser may consume per turn (0 = default 4m)
	Metrics    *obs.Metrics
}

func (m *Tools) Name() string { return "tools-mid" }

func (m *Tools) Handle(rc *agent.RunContext, next func(*agent.RunContext) error) error {
	hist := getHistory(rc)

	// Resume path: fold the user's clarify answer into history and clear state.
	if isResumed(rc) {
		if _, pending := getPendingClarify(rc); pending {
			if ans := lastUserMessage(rc.Messages); ans != "" {
				hist = append(hist, llm.ChatMessage{Role: llm.RoleUser, Content: ans})
			}
			delete(rc.Vars, VarPendingClarify)
		}
		delete(rc.Vars, VarResumeKey)
	}

	specs := toolSpecs(rc.Tools)
	maxIters := m.MaxIters
	if maxIters <= 0 {
		maxIters = 8
	}
	var guiNanos int64 // cumulative browser wall-time across this turn

	for iter := 0; iter < maxIters; iter++ {
		if err := rc.Ctx.Err(); err != nil {
			setHistory(rc, hist)
			return err
		}
		if m.MaxHistory > 0 {
			hist, _ = trimHistory(hist, m.MaxHistory)
		}
		// Budget-aware nudge: two iterations before the cap, tell the model to
		// stop researching and produce the deliverable (write any required file)
		// NOW, while tools are still available — otherwise a thorough run can
		// exhaust the loop on data gathering and never reach its output step.
		if iter == maxIters-2 {
			hist = append(hist, llm.ChatMessage{
				Role:    llm.RoleUser,
				Content: "⚠️ 工具调用预算即将用完。请立刻停止额外调研,用现有信息完成任务的最终产出:如果任务要求写文件,现在就调用 file_write 写入,然后给出简短总结。",
			})
		}
		req := llm.Request{Model: m.Model, Messages: hist, Tools: specs}
		var resp llm.Response
		var err error
		if sc, ok := m.LLM.(llm.StreamingClient); ok {
			// Stream content deltas to the client for live rendering; the final
			// EventChat (emitted by output-mid) remains the persisted source.
			resp, err = sc.ChatStream(rc.Ctx, req, func(delta string) {
				rc.Emit(messages.StreamDelta(delta, rc.Meta))
			})
		} else {
			resp, err = m.LLM.Chat(rc.Ctx, req)
		}
		if err != nil {
			setHistory(rc, hist)
			return fmt.Errorf("llm chat: %w", err)
		}
		if m.Metrics != nil && resp.Usage.TotalTokens > 0 {
			m.Metrics.ObserveLLM(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		}

		if len(resp.ToolCalls) == 0 {
			hist = append(hist, llm.ChatMessage{Role: llm.RoleAssistant, Content: resp.Content})
			rc.Vars[VarFinal] = resp.Content
			setHistory(rc, hist)
			return nil
		}

		hist = append(hist, llm.ChatMessage{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})

		// clarify takes priority over everything else and interrupts the batch.
		// CONTRACT: the assistant message above carries ALL tool_calls, and strict
		// providers require every tool_call to have a matching tool reply. So when
		// clarify co-occurs with sibling calls, we still append a tool reply for
		// EVERY tool_call (clarify → "asked the user"; siblings → "deferred, will
		// redo after the reply") before interrupting — otherwise resume sends an
		// assistant message with dangling tool_calls and the provider rejects it.
		// (This is amplified under multi-agent, where batches mix sub-agent calls
		// with clarify far more often.)
		if clarTC, ok := findClarify(resp.ToolCalls); ok {
			for _, tc := range resp.ToolCalls {
				reply := "本步因需要用户澄清而暂缓,将在用户回复后重做。"
				if tc.ID == clarTC.ID {
					reply = "已向用户提出澄清问题,等待回复。"
				}
				hist = append(hist, llm.ChatMessage{Role: llm.RoleTool, ToolCallID: tc.ID, Name: tc.Name, Content: reply})
			}
			clar := parseClarify(clarTC.Arguments)
			rc.Vars[VarPendingClarify] = clar
			rc.Interrupt = &agent.Interrupt{Reason: "clarify", Clarify: &clar}
			setHistory(rc, hist)
			return nil // cursor stays here; resume re-enters this loop
		}

		// Execute the batch's tool calls concurrently (bounded), then emit +
		// fold results into history in the original order (so the assistant
		// tool_calls and their tool replies stay correctly paired/ordered).
		results := m.runBatch(rc, resp.ToolCalls, &guiNanos)
		for _, r := range results {
			if r.skill {
				if r.skillOK {
					rc.Emit(messages.New(messages.EventSkill, messages.RoleSystem, rc.Meta))
				}
				rc.Emit(messages.Tool("call", map[string]any{"tool": r.name, "args": r.args, "result": r.content}, rc.Meta))
			} else {
				payload := map[string]any{"tool": r.name, "args": r.args}
				if r.errStr != "" {
					payload["error"] = r.errStr
				} else {
					payload["result"] = r.content
				}
				rc.Emit(messages.Tool("call", payload, rc.Meta))
			}
			hist = append(hist, llm.ChatMessage{Role: llm.RoleTool, ToolCallID: r.id, Name: r.name, Content: r.content})
		}
	}

	// Hit the iteration cap: force one final answer WITHOUT tools so the user
	// gets a useful summary of what was gathered instead of a raw stop message.
	hist = append(hist, llm.ChatMessage{
		Role: llm.RoleUser,
		Content: "基于以上工具返回的信息,直接给出你能给出的最佳回答;若信息不完整,简要说明并给出已知部分。不要再调用任何工具。",
	})
	if resp, err := m.LLM.Chat(rc.Ctx, llm.Request{Model: m.Model, Messages: hist}); err == nil && resp.Content != "" {
		hist = append(hist, llm.ChatMessage{Role: llm.RoleAssistant, Content: resp.Content})
		rc.Vars[VarFinal] = resp.Content
	} else {
		rc.Vars[VarFinal] = "我尝试了多次但没能拿到完整结果,请换个问法或稍后再试。"
	}
	setHistory(rc, hist)
	return nil
}

// toolResult is one tool call's outcome, carried back from runBatch in order.
type toolResult struct {
	id      string
	name    string
	args    map[string]any
	content string
	errStr  string
	skill   bool // apply_skill (handled in-process)
	skillOK bool
}

// guiToolName is the GUI browser tool; it is expensive and prone to flailing on
// JS-heavy pages, so its invocations are capped per chat turn.
const guiToolName = "run_agent"

// runBatch executes the tool calls concurrently (bounded by maxConcurrent) and
// returns results in the same order as calls. Invocations run in parallel; the
// caller emits events + appends to history sequentially to preserve ordering and
// avoid interleaving on the SSE pipe. apply_skill is resolved in-process.
//
// guiNanos (shared across the whole turn) tracks the cumulative wall-time the
// browser has consumed. Once it exceeds GUIBudget, further run_agent calls are
// short-circuited with a directive to answer from gathered info — this caps a
// flailing browser task by TIME, so many short, productive calls are fine while
// only runaway looping is curbed.
func (m *Tools) runBatch(rc *agent.RunContext, calls []llm.ToolCall, guiNanos *int64) []toolResult {
	const maxConcurrent = 4
	budget := m.GUIBudget
	if budget <= 0 {
		budget = 4 * time.Minute
	}
	results := make([]toolResult, len(calls))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex // guards *guiNanos

	for i := range calls {
		tc := calls[i]
		r := &results[i]
		r.id, r.name = tc.ID, tc.Name

		if tc.Name == ApplySkillTool {
			name := fmt.Sprint(parseArgs(tc.Arguments)["name"])
			r.args = map[string]any{"name": name}
			r.content, r.skillOK = applySkill(name)
			r.skill = true
			continue
		}

		r.args = parseArgs(tc.Arguments)

		// Cumulative GUI time budget (checked sequentially before the concurrent
		// invokes, so the read is consistent within a batch).
		isGUI := tc.Name == guiToolName
		if isGUI && *guiNanos >= int64(budget) {
			r.content = "浏览器(run_agent)本轮已累计运行较久,达到时间预算。请不要再调用 run_agent;" +
				"用已经获取到的页面信息直接给出最佳回答,信息不足就如实说明并给出已知部分。"
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(r *toolResult, isGUI bool) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			out, err := m.invoke(rc, r.name, r.args)
			if isGUI {
				mu.Lock()
				*guiNanos += time.Since(start).Nanoseconds()
				mu.Unlock()
			}
			if err != nil {
				r.errStr = err.Error()
				r.content = "ERROR: " + err.Error()
				return
			}
			r.content = out
		}(r, isGUI)
	}
	wg.Wait()
	return results
}

func (m *Tools) invoke(rc *agent.RunContext, name string, args map[string]any) (string, error) {
	tool := findTool(rc.Tools, name)
	if tool == nil {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	ctx, end := trace.StartSpan(rc.Ctx, "tool.invoke", map[string]string{"tool": name})
	defer end()
	start := time.Now()
	out, err := tool.Invoke(ctx, args)
	if m.Metrics != nil {
		m.Metrics.ObserveToolCall(time.Since(start).Nanoseconds())
	}
	return out, err
}

func findTool(tools []agent.BaseTool, name string) agent.BaseTool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// toolSpecs converts BaseTools to LLM specs and appends the built-in clarify tool.
func toolSpecs(tools []agent.BaseTool) []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(tools)+1)
	for _, t := range tools {
		specs = append(specs, llm.ToolSpec{Name: t.Name(), Description: t.Description(), Parameters: t.Schema()})
	}
	specs = append(specs, llm.ToolSpec{
		Name:        ClarifyToolName,
		Description: "Ask the user a concise clarifying question when the request is ambiguous or missing information.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "the question to ask"},
				"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "optional choices"},
			},
			"required": []string{"question"},
		},
	})
	specs = append(specs, llm.ToolSpec{
		Name:        ApplySkillTool,
		Description: "Adopt a domain skill (expertise prompt pack) before answering. " + strings.Join(skillNames(), ", ") + ".",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string", "enum": skillNames(), "description": "the skill to adopt"},
			},
			"required": []string{"name"},
		},
	})
	return specs
}

// findClarify returns the first clarify tool call in a batch, if any.
func findClarify(calls []llm.ToolCall) (llm.ToolCall, bool) {
	for _, tc := range calls {
		if tc.Name == ClarifyToolName {
			return tc, true
		}
	}
	return llm.ToolCall{}, false
}

func parseArgs(s string) map[string]any {
	out := map[string]any{}
	if s == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		// Malformed tool arguments from the model: log it (a common cause of
		// "the tool did nothing") rather than silently swallowing.
		slog.Warn("tools-mid: malformed tool arguments", "err", err, "raw", truncStr(s, 200))
	}
	return out
}

func parseClarify(s string) messages.ClarifyMessage {
	var raw struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Context  string   `json:"context"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		slog.Warn("tools-mid: malformed clarify arguments", "err", err, "raw", truncStr(s, 200))
	}
	return messages.ClarifyMessage{Question: raw.Question, Options: raw.Options, Context: raw.Context}
}

func truncStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

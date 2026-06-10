package middlewares

import (
	"encoding/json"
	"fmt"
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
	MaxHistory int // bound context across ReAct iterations (0 = unbounded)
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

	for iter := 0; iter < maxIters; iter++ {
		if err := rc.Ctx.Err(); err != nil {
			setHistory(rc, hist)
			return err
		}
		if m.MaxHistory > 0 {
			hist, _ = trimHistory(hist, m.MaxHistory)
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

		if len(resp.ToolCalls) == 0 {
			hist = append(hist, llm.ChatMessage{Role: llm.RoleAssistant, Content: resp.Content})
			rc.Vars[VarFinal] = resp.Content
			setHistory(rc, hist)
			return nil
		}

		hist = append(hist, llm.ChatMessage{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: resp.ToolCalls})

		// clarify takes priority over everything else and interrupts the batch.
		for _, tc := range resp.ToolCalls {
			if tc.Name == ClarifyToolName {
				clar := parseClarify(tc.Arguments)
				rc.Vars[VarPendingClarify] = clar
				rc.Interrupt = &agent.Interrupt{Reason: "clarify", Clarify: &clar}
				setHistory(rc, hist)
				return nil // cursor stays here; resume re-enters this loop
			}
		}

		// Execute the batch's tool calls concurrently (bounded), then emit +
		// fold results into history in the original order (so the assistant
		// tool_calls and their tool replies stay correctly paired/ordered).
		results := m.runBatch(rc, resp.ToolCalls)
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

// runBatch executes the tool calls concurrently (bounded by maxConcurrent) and
// returns results in the same order as calls. Invocations run in parallel; the
// caller emits events + appends to history sequentially to preserve ordering and
// avoid interleaving on the SSE pipe. apply_skill is resolved in-process.
func (m *Tools) runBatch(rc *agent.RunContext, calls []llm.ToolCall) []toolResult {
	const maxConcurrent = 4
	results := make([]toolResult, len(calls))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

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
		wg.Add(1)
		sem <- struct{}{}
		go func(r *toolResult) {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := m.invoke(rc, r.name, r.args)
			if err != nil {
				r.errStr = err.Error()
				r.content = "ERROR: " + err.Error()
				return
			}
			r.content = out
		}(r)
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

func parseArgs(s string) map[string]any {
	out := map[string]any{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func parseClarify(s string) messages.ClarifyMessage {
	var raw struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Context  string   `json:"context"`
	}
	_ = json.Unmarshal([]byte(s), &raw)
	return messages.ClarifyMessage{Question: raw.Question, Options: raw.Options, Context: raw.Context}
}

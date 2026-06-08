// Package middlewares implements the pipeline stages that run inside the agent
// runtime. Each stage implements cavis_core/agent.Middleware. State is passed
// between stages via RunContext.Vars under the Var* keys defined here.
package middlewares

import (
	"encoding/json"

	"github.com/cavis-oss/cavis_core/agent"
	"github.com/cavis-oss/cavis_core/messages"
	"github.com/cavis-oss/cavis_control_layer/llm"
)

// Var keys shared across middlewares.
const (
	VarLLMHistory     = "llm_history"     // []llm.ChatMessage
	VarFinal          = "final"           // string: final assistant content
	VarPlan           = "plan"            // string: generated plan
	VarPendingClarify = "pending_clarify" // messages.ClarifyMessage
	VarResumeKey      = "resume_key"      // string: set by runner on resume
)

// ClarifyToolName is the built-in tool the model calls to ask the user.
const ClarifyToolName = "clarify"

// DefaultSystemPrompt is used when none is configured.
const DefaultSystemPrompt = "You are Cavis, a helpful enterprise AI agent. " +
	"Use the provided tools when they help, and choose the lightest tool for the job:\n" +
	"- For facts, news, prices, definitions: use `web_search` (then `fetch_url` to read a result).\n" +
	"- For weather: use `weather`.\n" +
	"- For reading/writing the user's files: use the `file_*` tools.\n" +
	"- Use `run_agent` (the GUI browser) ONLY for tasks that truly require interacting " +
	"with a web page (logging in, clicking, filling forms). Never use it just to look up " +
	"information — that is what web_search/fetch_url are for.\n" +
	"If the request is ambiguous or missing required info, call `clarify` to ask a concise " +
	"question instead of guessing. Answer in the user's language."

// getHistory reads the LLM history from Vars, tolerating a JSON-restored value
// (e.g. after a checkpoint round-trip where the concrete type is lost).
func getHistory(rc *agent.RunContext) []llm.ChatMessage {
	v, ok := rc.Vars[VarLLMHistory]
	if !ok {
		return nil
	}
	if h, ok := v.([]llm.ChatMessage); ok {
		return h
	}
	return redecode[[]llm.ChatMessage](v)
}

func setHistory(rc *agent.RunContext, h []llm.ChatMessage) {
	rc.Vars[VarLLMHistory] = h
}

// getPendingClarify reads a pending clarify, tolerating JSON-restored values.
func getPendingClarify(rc *agent.RunContext) (messages.ClarifyMessage, bool) {
	v, ok := rc.Vars[VarPendingClarify]
	if !ok {
		return messages.ClarifyMessage{}, false
	}
	if c, ok := v.(messages.ClarifyMessage); ok {
		return c, true
	}
	return redecode[messages.ClarifyMessage](v), true
}

// isResumed reports whether the runner injected a resume.
func isResumed(rc *agent.RunContext) bool {
	_, ok := rc.Vars[VarResumeKey]
	return ok
}

// lastUserMessage returns the content of the most recent user chat message.
func lastUserMessage(msgs []messages.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Type == messages.EventChat && msgs[i].Role == messages.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}

// redecode marshals v to JSON and back into type T (used to recover concrete
// types from interface{} values restored from a checkpoint).
func redecode[T any](v any) T {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

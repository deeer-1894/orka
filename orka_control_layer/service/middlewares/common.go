// Package middlewares implements the pipeline stages that run inside the agent
// runtime. Each stage implements orka_core/agent.Middleware. State is passed
// between stages via RunContext.Vars under the Var* keys defined here.
package middlewares

import (
	"encoding/json"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
)

// Var keys shared across middlewares.
const (
	VarLLMHistory     = "llm_history"     // []llm.ChatMessage
	VarFinal          = "final"           // string: final assistant content
	VarPlan           = "plan"            // string: generated plan
	VarPendingClarify = "pending_clarify" // messages.ClarifyMessage
	VarResumeKey      = "resume_key"      // string: set by runner on resume
	VarRunTokens      = "run_tokens"      // int: total tokens this run
	VarRunTools       = "run_tools"       // int: total tool calls this run
)

// Typed, named accessors for the shared vars. Prefer these over raw
// rc.Vars[...] / .(T) at call sites: they centralize the keys (so a typo is a
// compile error, not a silent miss) and use comma-ok assertions (so a wrong type
// degrades to a zero value instead of panicking).

// Final / SetFinal carry the final assistant content (the run's output).
func Final(rc *agent.RunContext) string       { return rc.Str(VarFinal) }
func SetFinal(rc *agent.RunContext, s string) { rc.Put(VarFinal, s) }

// RunTokens / AddRunTokens accumulate the token usage across a run.
func RunTokens(rc *agent.RunContext) int       { return rc.IntVar(VarRunTokens) }
func AddRunTokens(rc *agent.RunContext, n int) { rc.Put(VarRunTokens, rc.IntVar(VarRunTokens)+n) }

// RunTools / AddRunTools accumulate the tool-call count across a run.
func RunTools(rc *agent.RunContext) int       { return rc.IntVar(VarRunTools) }
func AddRunTools(rc *agent.RunContext, n int) { rc.Put(VarRunTools, rc.IntVar(VarRunTools)+n) }

// ClarifyToolName is the built-in tool the model calls to ask the user.
const ClarifyToolName = "clarify"

// DefaultSystemPrompt is used when none is configured.
const DefaultSystemPrompt = "You are Orka, a helpful enterprise AI agent. " +
	"Use the provided tools when they help, and choose the lightest tool for the job:\n" +
	"- For facts, news, prices, definitions: use `web_search` (then `fetch_url` to read a result).\n" +
	"- For weather: use `weather`.\n" +
	"- For reading/writing the user's files: use the `file_*` tools. Pass a plain " +
	"relative filename like `report.md` — never an absolute path or leading slash; " +
	"your files already live at the workspace root.\n" +
	"- When a command line would do the job (running a script or code you wrote, git, " +
	"data wrangling with CLI tools, file conversions, installing a package), use the `shell` " +
	"tool if it is available — it is a real terminal in your workspace. Prefer writing code to " +
	"a file and running it over doing complex transformations by hand.\n" +
	"- Decide which tool fits from the task itself — never wait for the user to name a tool. " +
	"Use `web_search`/`fetch_url` for plain information lookups. Reach for `run_agent` (the GUI " +
	"browser) ON YOUR OWN whenever the task needs real page interaction (logging in, clicking, " +
	"filling forms, navigating a JavaScript-heavy or dynamic site), AND escalate to it automatically " +
	"when `web_search`/`fetch_url` fail, are blocked, or return nothing useful. The browser is slower, " +
	"so prefer search for a lookup it can already answer — but never stall or ask the user which tool " +
	"to use when the browser would get the job done.\n" +
	"If the request is ambiguous or missing required info, call `clarify` to ask a concise " +
	"question instead of guessing.\n" +
	"For a complex or multi-step request, FIRST call the `update_plan` tool with a short checklist " +
	"(3–6 steps, all status \"pending\") so the user can follow along; then carry it out, marking the step you're on as \"active\" and finished steps as \"done\" via more `update_plan` calls — calling tools as needed and adjusting " +
	"the plan if you learn something new along the way. For a simple one-step request, skip the " +
	"plan and just answer.\n" +
	"Answer in the user's language."

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

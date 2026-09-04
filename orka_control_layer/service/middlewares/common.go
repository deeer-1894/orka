// Package middlewares implements the pipeline stages that run inside the agent
// runtime. Each stage implements orka_core/agent.Middleware. State is passed
// between stages via RunContext.Vars under the Var* keys defined here.
package middlewares

import (
	"github.com/orka-oss/orka_core/agent"
)

// Var keys shared across middlewares.
const (
	VarFinal     = "final"      // string: final assistant content
	VarRunTokens = "run_tokens" // int: total tokens this run
	VarRunTools  = "run_tools"  // int: total tool calls this run
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
	// Batching is stated up front because it is the cheapest latency win
	// available: independent calls emitted together execute concurrently, while
	// one-per-turn pays a full model round-trip each — 15-25 seconds apiece on
	// this deployment. Measured before this line existed: 1.65 tool calls per
	// turn, i.e. mostly one at a time.
	"When several tool calls do not depend on each other, emit them TOGETHER in one turn — they run " +
	"concurrently. Only sequence a call when its input genuinely comes from a previous result.\n" +
	"Use the provided tools when they help, and choose the lightest tool for the job:\n" +
	// The failure this prevents: asked to RESEARCH five databases and write a
	// comparison report, the agent produced seven files and 4.9KB of confident,
	// plausible prose with ZERO retrieval calls — entirely from memory. Nothing
	// in it was wrong enough to notice, and nothing in it was checked. A report
	// that merely looks researched is worse than a slow one, because the reader
	// cannot tell the difference.
	"- WHEN THE USER ASKS YOU TO RESEARCH, LOOK UP, VERIFY, COMPARE or REPORT ON something, you MUST " +
	"actually retrieve — do not answer from memory even when you believe you know it. Your training " +
	"data is stale and the user asked for research, not recollection. In any document you produce " +
	"this way, cite the source next to each substantive claim. If you genuinely cannot retrieve, say " +
	"so plainly in the document rather than writing unsourced prose that reads as if you had.\n" +
	// Measured on a 178-tool-call research run: the agent wrote ONE file the whole
	// time (the final report) and issued zero writes across 30 shell/python calls,
	// while spending 101 calls re-reading tool output the context manager had
	// evicted to disk. It was using the eviction archive as working memory —
	// raw fetched pages, ~5k tokens a piece, paged back in one at a time — and 24
	// of those reads were of a path it had already read. Nothing told it to keep
	// notes, and a conclusion it never wrote down is a conclusion it must re-derive
	// from the source every time.
	"- On a research or multi-source task, write each finding down AS YOU GET IT: append a one-line " +
	"conclusion plus its source link to `notes.md` with `file_write`, then work from your notes. " +
	"Never re-read a page you have already read — if you need it again, the note is what you needed.\n" +
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
	"the plan if you learn something new along the way. While work remains, put each `update_plan` in " +
	"the SAME batch as that step's actual work rather than spending a turn on the checklist alone — " +
	"except for the LAST one: before your final answer, call `update_plan` once more with every step " +
	"\"done\", or the run is recorded as incomplete however much you finished. For a simple one-step " +
	"request, skip the plan and just answer.\n" +
	"Answer in the user's language."

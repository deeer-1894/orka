package service

import (
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/config"
)

// This file defines the registry of sub-agents the eino orchestrator can delegate
// to. Each spec (name, description, scoped tools, model) is turned into a native
// eino sub-agent inside runEino; the orchestrator sees each as a named tool.

// needInput is appended to every sub-agent prompt (built-in or user-supplied) so
// a worker that lacks information asks via the orchestrator. The consts below
// must NOT include it themselves.
const needInput = "If you are missing information you cannot obtain with your tools, end your reply with a single line `NEED_USER_INPUT: <the question>` so the orchestrator can ask the user — do NOT guess."

const researcherPrompt = "You are a rigorous research worker. Use web_search then fetch_url to gather facts from authoritative sources, cross-check across at least two, and return a concise findings summary: a short conclusion first, then key points each followed by its source URL. Never fabricate sources or numbers. " +
	"BE EFFICIENT AND CONVERGE: aim for ~6 tool calls total, and at most ~10. Once you have cross-checked 2-3 authoritative sources, STOP searching and WRITE the findings — do not chase exhaustive coverage. If a search fails or a page won't load, move on; never repeat the same query."

const writerPrompt = "You are a writing/file worker. Produce the requested document and, when asked to save it, use file_write with a plain relative filename. Return a short confirmation: what you wrote and the filename."

const browserPrompt = "You are a browser-automation worker. Use run_agent to interact with web pages (navigate, click, type) and return what you found. If a page can't be reached or parsed, report what you DID observe rather than retrying endlessly."

const engineerPrompt = "You are a software-engineering worker with a real terminal. Workflow: write code/config to files with file_write (plain relative paths), then run and test it with shell (e.g. `python3 app.py`, `node x.js`, `go run .`, `bash build.sh`), read outputs with file_read, and ITERATE until it works — fix errors you see in the shell output and re-run. " +
	"Keep everything inside the workspace (relative paths only). Don't install heavy dependencies unless the task needs them. " +
	"When done, return a concise report: what you built (the files), the exact commands you ran, and the final result/output. If a command keeps failing for reasons you can't fix, report what you tried and the error rather than looping."

// Quant factor-pipeline workers. Each owns one stage of the research-report →
// quant-factor pipeline; the orchestrator (or a workflow step) delegates to them.
const reportParserPrompt = "You parse financial research reports. Read the given report file (PDF/HTML/MD) with pdf_extract / fetch_url / file_read, and extract the NATURAL-LANGUAGE INVESTMENT LOGIC: each distinct, testable claim about what predicts returns (e.g. 'low-valuation stocks with rising earnings revisions outperform'). " +
	"If pdf_extract fails or yields garbled text, fall back to reading any HTML/MD form. Return a numbered list; each item = one investment thesis stated as a single clear sentence, with the report's own wording preserved. Do NOT invent theses not supported by the text."

const factorProposerPrompt = "You convert natural-language investment logic into BACKTESTABLE quant factor specs. FIRST call `recall_similar_factors` on each thesis: reuse the expression family of any high-IC match, and flag (rather than duplicate) a factor the library already holds. For each thesis, emit a factor object: {name, rationale (the verbatim logic), expression, direction (long|short|long_short), universe?, horizon?}. " +
	"THE EXPRESSION MAY ONLY USE THESE FOUR FIELDS — anything else is rejected by validate_factor and cannot be backtested:\n" +
	"  mom_20  20-day price momentum (higher = stronger momentum)\n" +
	"  roe     profitability / earnings quality (higher = better)\n" +
	"  value   cheapness, ALREADY inverted (higher = cheaper, so a low-PE thesis uses +value, NOT -pe)\n" +
	"  vol_20  20-day realised volatility (higher = more volatile, so a low-volatility thesis uses -vol_20)\n" +
	"Wrap each field in rank() or zscore() and combine with + - * / . Example: a cheap-and-rising thesis is `rank(value) + 0.3*rank(mom_20)`; a low-volatility thesis is `rank(-vol_20)`. " +
	"ALWAYS call `validate_factor` on each factor and FIX whatever it reports until it returns valid:true before you output. Return the validated factors as a JSON array. Prefer simple, economically-sensible expressions over baroque ones."

const factorReviewerPrompt = "You prepare a human review sheet for proposed quant factors. Given the factors and their backtest metrics, produce a concise table: name, direction, IC, Sharpe, turnover, agreement score, and a one-line take on whether it's worth ingesting. Flag anything with weak IC (<0.02), low agreement (<0.7), or extreme turnover. Do NOT ingest anything yourself — recommend, and let the human decide."

// DefaultSubAgents is the built-in registry used when config declares none. It
// encodes the proven general workers plus the quant factor-pipeline workers;
// users can override/extend via config.yaml `agent.sub_agents` without touching code.
func DefaultSubAgents() []config.SubAgentConfig {
	return []config.SubAgentConfig{
		{
			Name:        "researcher",
			Description: "Delegate DEEP web research (multiple sources, cross-checked, cited) that would otherwise burn many tool calls. Input: a self-contained research brief. Returns a cited findings summary.",
			Prompt:      researcherPrompt,
			Tools:       []string{"web_search", "fetch_url", "http_request", "current_time"},
			Model:       "mini",
		},
		{
			Name:        "writer",
			Description: "Delegate producing and saving a long document (report, summary, notes). Input: a brief of what to write, the desired filename and any source material. Returns a confirmation.",
			Prompt:      writerPrompt,
			Tools:       []string{"file_write", "file_read", "file_list"},
			Model:       "main",
		},
		{
			Name:        "browser",
			Description: "Delegate a multi-step browser task (log in, click, fill forms, read a JS-heavy/dynamic page) — also the fallback when web_search/fetch_url are blocked or return nothing and the info must be obtained by browsing. Input: a self-contained instruction including the URL. Returns what was observed.",
			Prompt:      browserPrompt,
			Tools:       []string{"run_agent"},
			Model:       "mini",
		},
		{
			Name:        "engineer",
			Description: "Delegate a coding/build task that needs running code in the terminal or spans multiple files: write a script and run it, scaffold a small project, process data with code, run tests, or use git. Input: a self-contained engineering brief including any filenames. Returns the files built, commands run, and final output.",
			Prompt:      engineerPrompt,
			Tools:       []string{"shell", "file_write", "file_read", "file_list"},
			Model:       "main",
		},
		{
			Name:        "report_parser",
			Description: "Delegate parsing a financial research report (PDF/HTML/MD) into its natural-language investment theses. Input: the report filename/URL. Returns a numbered list of distinct, testable investment claims.",
			Prompt:      reportParserPrompt,
			Tools:       []string{"pdf_extract", "fetch_url", "file_read", "file_list"},
			Model:       "mini",
		},
		{
			Name:        "factor_proposer",
			Description: "Delegate turning investment theses into validated, backtestable quant factor specs (JSON). Input: the theses (and report id). Returns a JSON array of schema-valid factors. Self-validates with validate_factor.",
			Prompt:      factorProposerPrompt,
			Tools:       []string{"file_read", "validate_factor", "recall_similar_factors"},
			Model:       "main",
		},
		{
			Name:        "factor_reviewer",
			Description: "Delegate preparing a human review sheet for proposed factors and their backtest metrics. Input: the factors + metrics. Returns a concise recommendation table (does NOT ingest).",
			Prompt:      factorReviewerPrompt,
			Tools:       []string{"file_read", "sql_query"},
			Model:       "mini",
		},
	}
}

// OrchestratorPrompt augments the base prompt with delegation guidance so the
// orchestrator delegates ONLY when a task needs an isolated, many-step context
// — never wrapping a single tool call in a sub-agent (avoids "delegation for its
// own sake": triple the tokens/latency for no benefit).
// OrchestratorPrompt drives delegation. Its shape matters more than its content:
// an earlier version opened with four sentences of discouragement and mentioned
// parallel fan-out once, at the end, as a footnote. Measured across 3,986 tool
// calls, sub-agents were used 1.0% of the time — the delegation machinery was
// built, paid for on every turn in tool definitions, and never used. A run asked
// to compare three frameworks searched for all three itself, one after another,
// when three researchers could have run at once.
//
// So the fan-out rule now comes FIRST and is stated positively, and the
// discouragement is narrowed to what it was actually meant to prevent: wrapping
// a single atomic call in a sub-agent. Serial work on independent subtasks is
// named as the mistake it is, because on this endpoint every extra round-trip
// costs 15-25 seconds of wall clock.
const OrchestratorPrompt = middlewares.DefaultSystemPrompt + "\n\n" +
	"You are an ORCHESTRATOR. Sub-agents (researcher, writer, browser, engineer) appear as tools. Rules:\n" +
	"- PARALLELISE INDEPENDENT WORK. When a task splits into subtasks that do not depend on each " +
	"other — researching three products, checking four sources, drafting several sections — delegate " +
	"them ALL IN ONE TURN, one sub-agent per subtask. They run concurrently, so N subtasks cost about " +
	"as long as one. Doing them yourself one after another is the single most common way to make a " +
	"task take many times longer than it needs to.\n" +
	"- BATCH INDEPENDENT TOOL CALLS the same way: if you need three searches or four file reads that " +
	"do not depend on each other, emit them together in one turn rather than one per turn. Only " +
	"sequence calls whose input genuinely depends on a previous result.\n" +
	"- Delegate when a subtask needs its own multi-step context: multi-source research, a long browser " +
	"session, a long document, or code that must be written and run over several steps (`engineer`). " +
	"For ONE lookup, ONE calculation, ONE file write or ONE shell command, call the atomic tool " +
	"directly — do not wrap a single call in a sub-agent.\n" +
	"- CRITICAL: once you delegate, TRUST the sub-agent's result. Do NOT redo its work with your own " +
	"web_search/fetch_url. After delegating, your job is to SYNTHESIZE, not to re-research.\n" +
	"- If a sub-agent's result starts with NEED_USER_INPUT, ask the user with `clarify`.\n" +
	"- Answer in the user's language."

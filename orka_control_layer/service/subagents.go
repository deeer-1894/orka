package service

import (
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/obs"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// This file generalizes the proven gui_agent pattern (an isolated worker invoked
// as a tool, whose events bubble up) into a small registry of sub-agents, in the
// Eino host/supervisor shape: the orchestrator sees each as a named tool and
// delegates to it. A sub-agent runs in its own context, burns its own tool
// budget, and returns a compact result — keeping the orchestrator's context clean.

// finalOf reads a sub-agent's final answer from its RunContext (set by tools-mid).
func finalOf(rc *agent.RunContext) string {
	if v, ok := rc.Vars[middlewares.VarFinal].(string); ok {
		return v
	}
	return ""
}

// subAgentRunner builds a worker pipeline: setup(role prompt) → memory → tools.
// No output-mid (the answer is returned via AgentTool.Final, not emitted as a
// chat bubble), NoClarify (workers return NEED_USER_INPUT instead of asking the
// user directly), NoStream (workers don't hijack the orchestrator's main bubble).
func subAgentRunner(prompt string, c llm.Client, model string, metrics *obs.Metrics) func([]agent.BaseTool) agent.Runner {
	return func([]agent.BaseTool) agent.Runner {
		return agent.NewDefaultRunner(
			&middlewares.Setup{SystemPrompt: prompt},
			&middlewares.Memory{MaxMessages: 40},
			&middlewares.Tools{
				LLM: c, Model: model, Metrics: metrics,
				MaxHistory: 40, MaxIters: 12, NoClarify: true, NoStream: true,
			},
		)
	}
}

const needInput = "If you are missing information you cannot obtain with your tools, end your reply with a single line `NEED_USER_INPUT: <the question>` so the orchestrator can ask the user — do NOT guess."

const researcherPrompt = "You are a rigorous research worker. Use web_search then fetch_url to gather facts from authoritative sources, cross-check across at least two, and return a concise findings summary: a short conclusion first, then key points each followed by its source URL. Never fabricate sources or numbers. " + needInput

const writerPrompt = "You are a writing/file worker. Produce the requested document and, when asked to save it, use file_write with a plain relative filename. Return a short confirmation: what you wrote and the filename. " + needInput

const browserPrompt = "You are a browser-automation worker. Use run_agent to interact with web pages (navigate, click, type) and return what you found. If a page can't be reached or parsed, report what you DID observe rather than retrying endlessly. " + needInput

// BuildSubAgents returns the orchestrator-facing sub-agent tools, each given a
// scoped subset of the available atomic tools. Workers use the mini model;
// the orchestrator (caller) uses the main model.
func BuildSubAgents(available []agent.BaseTool, main, mini llm.Client, mainModel, miniModel string, metrics *obs.Metrics) []agent.BaseTool {
	byName := map[string]agent.BaseTool{}
	for _, t := range available {
		byName[t.Name()] = t
	}
	pick := func(names ...string) []agent.BaseTool {
		var out []agent.BaseTool
		for _, n := range names {
			if t, ok := byName[n]; ok {
				out = append(out, t)
			}
		}
		return out
	}

	var agents []agent.BaseTool

	if rs := pick("web_search", "fetch_url", "http_request", "current_time"); len(rs) > 0 {
		agents = append(agents, &agent.AgentTool{
			Spec: agent.AgentSpec{
				Name:        "researcher",
				Description: "Delegate DEEP web research (multiple sources, cross-checked, cited) that would otherwise burn many tool calls. Input: a self-contained research brief. Returns a cited findings summary.",
			},
			Tools: rs,
			Build: subAgentRunner(researcherPrompt, mini, miniModel, metrics),
			Final: finalOf,
		})
	}

	if ws := pick("file_write", "file_read", "file_list"); len(ws) > 0 {
		agents = append(agents, &agent.AgentTool{
			Spec: agent.AgentSpec{
				Name:        "writer",
				Description: "Delegate producing and saving a long document (report, summary, notes). Input: a brief of what to write, the desired filename and any source material. Returns a confirmation.",
			},
			Tools: ws,
			Build: subAgentRunner(writerPrompt, main, mainModel, metrics),
			Final: finalOf,
		})
	}

	if bs := pick("run_agent"); len(bs) > 0 {
		agents = append(agents, &agent.AgentTool{
			Spec: agent.AgentSpec{
				Name:        "browser",
				Description: "Delegate a multi-step browser task (log in, click, fill forms, read a JS-heavy page). Input: a self-contained instruction including the URL. Returns what was observed.",
			},
			Tools: bs,
			Build: subAgentRunner(browserPrompt, mini, miniModel, metrics),
			Final: finalOf,
		})
	}

	return agents
}

// OrchestratorPrompt augments the base prompt with delegation guidance so the
// orchestrator delegates ONLY when a task needs an isolated, many-step context
// — never wrapping a single tool call in a sub-agent (avoids "delegation for its
// own sake": triple the tokens/latency for no benefit).
const OrchestratorPrompt = middlewares.DefaultSystemPrompt + "\n\n" +
	"You can delegate to sub-agents (researcher, writer, browser) — they appear as tools. " +
	"Delegate ONLY when a sub-task genuinely needs its own multi-step context: deep multi-source " +
	"research, a long browser session, or producing a long document. For a single lookup, a quick " +
	"calculation, or one file write, call the atomic tool DIRECTLY — do not wrap it in a sub-agent. " +
	"You may delegate to several sub-agents in one turn; they run in parallel. After they return, " +
	"synthesize their results into the final answer yourself. If a sub-agent returns a line starting " +
	"with NEED_USER_INPUT, ask the user with `clarify`."

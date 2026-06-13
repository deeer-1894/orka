package service

import (
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
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
func subAgentRunner(prompt string, c llm.Client, model string, metrics *obs.Metrics, maxIters int) func([]agent.BaseTool) agent.Runner {
	if maxIters <= 0 {
		maxIters = 12
	}
	return func([]agent.BaseTool) agent.Runner {
		return agent.NewDefaultRunner(
			&middlewares.Setup{SystemPrompt: prompt},
			&middlewares.Memory{MaxMessages: 40},
			&middlewares.Tools{
				LLM: c, Model: model, Metrics: metrics,
				MaxHistory: 40, MaxIters: maxIters, NoClarify: true, NoStream: true,
			},
		)
	}
}

// needInput is appended to every sub-agent prompt (built-in or user-supplied) by
// BuildSubAgents, so the consts below must NOT include it themselves.
const needInput = "If you are missing information you cannot obtain with your tools, end your reply with a single line `NEED_USER_INPUT: <the question>` so the orchestrator can ask the user — do NOT guess."

const researcherPrompt = "You are a rigorous research worker. Use web_search then fetch_url to gather facts from authoritative sources, cross-check across at least two, and return a concise findings summary: a short conclusion first, then key points each followed by its source URL. Never fabricate sources or numbers. " +
	"BE EFFICIENT AND CONVERGE: aim for ~6 tool calls total, and at most ~10. Once you have cross-checked 2-3 authoritative sources, STOP searching and WRITE the findings — do not chase exhaustive coverage. If a search fails or a page won't load, move on; never repeat the same query."

const writerPrompt = "You are a writing/file worker. Produce the requested document and, when asked to save it, use file_write with a plain relative filename. Return a short confirmation: what you wrote and the filename."

const browserPrompt = "You are a browser-automation worker. Use run_agent to interact with web pages (navigate, click, type) and return what you found. If a page can't be reached or parsed, report what you DID observe rather than retrying endlessly."

// DefaultSubAgents is the built-in registry used when config declares none. It
// encodes the three proven workers; users can override/extend via config.yaml
// `agent.sub_agents` without touching code.
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
			Description: "Delegate a multi-step browser task (log in, click, fill forms, read a JS-heavy page). Input: a self-contained instruction including the URL. Returns what was observed.",
			Prompt:      browserPrompt,
			Tools:       []string{"run_agent"},
			Model:       "mini",
		},
	}
}

// BuildSubAgents turns sub-agent specs into orchestrator-facing tools, each
// scoped to the subset of available atomic tools it declares. A spec with
// Model "main" uses the orchestrator's model; anything else uses the mini
// model. Specs whose tools are all unavailable (e.g. run_agent when the GUI is
// off) are skipped. When specs is empty the built-in DefaultSubAgents is used.
func BuildSubAgents(available []agent.BaseTool, main, mini llm.Client, mainModel, miniModel string, metrics *obs.Metrics, specs []config.SubAgentConfig) []agent.BaseTool {
	if len(specs) == 0 {
		specs = DefaultSubAgents()
	}
	byName := map[string]agent.BaseTool{}
	for _, t := range available {
		byName[t.Name()] = t
	}
	pick := func(names []string) []agent.BaseTool {
		var out []agent.BaseTool
		for _, n := range names {
			if t, ok := byName[n]; ok {
				out = append(out, t)
			}
		}
		return out
	}

	var agents []agent.BaseTool
	for _, sp := range specs {
		if sp.Name == "" {
			continue
		}
		scoped := pick(sp.Tools)
		if len(scoped) == 0 {
			continue // none of this agent's tools are available; don't expose a dead agent
		}
		client, model := mini, miniModel
		if sp.Model == "main" {
			client, model = main, mainModel
		}
		prompt := sp.Prompt
		if prompt == "" {
			prompt = needInput
		} else {
			prompt += " " + needInput
		}
		agents = append(agents, &agent.AgentTool{
			Spec:  agent.AgentSpec{Name: sp.Name, Description: sp.Description},
			Tools: scoped,
			Build: subAgentRunner(prompt, client, model, metrics, sp.MaxIters),
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
	"You are an ORCHESTRATOR. You can delegate to sub-agents (researcher, writer, browser) — they " +
	"appear as tools. Rules:\n" +
	"- Delegate ONLY when a sub-task genuinely needs its own multi-step context: deep multi-source " +
	"research, a long browser session, or producing a long document. For a single lookup, a quick " +
	"calculation, or one file write, call the atomic tool DIRECTLY — never wrap it in a sub-agent.\n" +
	"- CRITICAL: once you delegate a sub-task, TRUST the sub-agent's returned result. Do NOT repeat " +
	"its work with your own web_search/fetch_url/etc. Your job after delegating is to SYNTHESIZE the " +
	"sub-agents' results into the final answer, not to re-research.\n" +
	"- You may delegate to several sub-agents in one turn; they run in parallel.\n" +
	"- If a sub-agent's result starts with NEED_USER_INPUT, ask the user with `clarify`.\n" +
	"- Answer in the user's language."

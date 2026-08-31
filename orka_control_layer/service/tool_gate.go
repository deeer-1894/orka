package service

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
)

// tool_gate.go — show the model the tools it plausibly needs, not all of them.
//
// Every turn used to carry the full definition of 49 tools: ~5,100 tokens
// before the model read a word of the question. Answering "what is idempotence"
// meant shipping the schemas for csv_join, sql_query, slides, xlsx_to_csv and
// pdf_extract, none of which could possibly be used. A multi-step task pays it
// again on every model round-trip, which is why one 7-tool eval task cost 55k
// tokens.
//
// The usage data says this is nearly all waste: across 3,880 recorded calls the
// top 6 tools account for 77%, the top 15 for 95%, and the 31 rarest for 2.8%
// between them. So the rare ones are not exposed up front — the agent asks for
// them by name with find_tools, and they stay available for the rest of the run.
//
// eino supports exactly this: BeforeModelRewriteState may rewrite
// state.ToolInfos, which its own docs call "the recommended approach for
// dynamic tool filtering/selection based on conversation context". The tools
// remain REGISTERED throughout, so anything already unlocked (or called from a
// replayed transcript) still executes — only their visibility changes.

// coreTools are always visible. Chosen from measured usage: these cover ~95% of
// all tool calls ever made here. Anything outside this set is one find_tools
// call away, which is a fair trade for not paying for it on every turn.
var coreTools = map[string]bool{
	// retrieval + the open web (77% of all calls between the first six)
	"web_search": true, "fetch_url": true, "http_request": true,
	// execution
	"shell": true, "python": true, "run_agent": true,
	// workspace
	"file_read": true, "file_write": true, "file_list": true, "file_delete": true,
	// control-plane affordances the agent needs to work well
	"update_plan": true, "clarify": true, "apply_skill": true, "find_skills": true,
	"artifact_publish": true,
	// small, constantly used utilities
	"current_time": true, "calculator": true,
	// The general-purpose sub-agents ARE the delegation mechanism in multi-agent
	// mode; hiding them would quietly turn it off. The quant-pipeline agents
	// (report_parser, factor_proposer, factor_reviewer) are deliberately absent —
	// they matter only inside that pipeline, which asks for them by name.
	"researcher": true, "writer": true, "browser": true, "engineer": true,
	// the gate's own escape hatch
	findToolsName: true,
}

const findToolsName = "find_tools"

// toolGate tracks which non-core tools this run has unlocked. One per run, on
// the run context, so a find_tools call and the middleware see the same set.
type toolGate struct {
	mu       sync.Mutex
	all      []*schema.ToolInfo // every registered tool, captured on the first call
	unlocked map[string]bool
}

func newToolGate() *toolGate { return &toolGate{unlocked: map[string]bool{}} }

// remember captures the complete tool list the first time the model is called.
// eino PERSISTS edits to state.ToolInfos across turns, so the full list is only
// visible once — after the first rewrite, state holds the filtered version.
func (g *toolGate) remember(infos []*schema.ToolInfo) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.all == nil && len(infos) > 0 {
		g.all = append([]*schema.ToolInfo(nil), infos...)
	}
}

// visible returns the tool infos for this turn: the core set plus whatever the
// run has unlocked, with find_tools carrying an index of what is hidden.
//
// The index is the part that makes this work. Hiding tools silently does not
// make the model ask for them — it makes the model improvise. Asked for a QR
// code with qrcode hidden, it went shell → python → pip install → python, four
// calls and a danger-tool approval, instead of the one call it had made a
// minute earlier. It never reached for find_tools, because it never believed
// the task was impossible.
//
// Names are cheap and schemas are not: listing 27 hidden tools costs ~110
// tokens against the ~2,900 their full definitions cost. So the model is told
// what exists and pays for the details only when it wants them.
func (g *toolGate) visible() []*schema.ToolInfo {
	g.mu.Lock()
	defer g.mu.Unlock()
	var hidden []string
	for _, ti := range g.all {
		if ti != nil && !coreTools[ti.Name] && !g.unlocked[ti.Name] {
			hidden = append(hidden, ti.Name)
		}
	}
	sort.Strings(hidden)

	out := make([]*schema.ToolInfo, 0, len(g.all))
	for _, ti := range g.all {
		if ti == nil {
			continue
		}
		if !coreTools[ti.Name] && !g.unlocked[ti.Name] {
			continue
		}
		if ti.Name == findToolsName && len(hidden) > 0 {
			// Copy: the entry is shared with the list captured in remember().
			idx := *ti
			idx.Desc = findToolsDesc(hidden)
			out = append(out, &idx)
			continue
		}
		out = append(out, ti)
	}
	return out
}

// findToolsDesc renders the escape hatch's description with the live index of
// hidden tools, so discovery does not depend on the model guessing.
func findToolsDesc(hidden []string) string {
	return "Enable one of the tools listed below, which are available but not yet loaded. " +
		"Call this FIRST whenever one of them fits the task — do NOT improvise with shell or python " +
		"to do something a listed tool already does. Pass a few words describing what you need; " +
		"matching tools become directly callable on your next step.\n" +
		"Available on request: " + strings.Join(hidden, ", ")
}

// unlock makes tools visible for the rest of the run and reports what it found.
func (g *toolGate) unlock(names []string) []*schema.ToolInfo {
	g.mu.Lock()
	defer g.mu.Unlock()
	var found []*schema.ToolInfo
	for _, ti := range g.all {
		if ti == nil {
			continue
		}
		for _, n := range names {
			if ti.Name == n {
				g.unlocked[ti.Name] = true
				found = append(found, ti)
				break
			}
		}
	}
	return found
}

// search finds hidden tools matching the query. Every term must match first, so
// "csv join" narrows rather than widens; if that finds nothing it falls back to
// the best partial match.
//
// The fallback is not a nicety. Asked for a QR code, the agent searched
// "generate QR code image" — and strict matching missed, because the tool's
// description never says "image". A miss costs a whole extra model round-trip,
// which is the entire budget this gate is trying to save.
func (g *toolGate) search(query string) []*schema.ToolInfo {
	terms := strings.Fields(strings.ToLower(query))
	g.mu.Lock()
	defer g.mu.Unlock()

	type scored struct {
		ti *schema.ToolInfo
		n  int
	}
	var all, partial []scored
	for _, ti := range g.all {
		if ti == nil || coreTools[ti.Name] {
			continue // core tools are already in front of the model
		}
		hay := strings.ToLower(ti.Name + " " + ti.Desc)
		n := 0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				n++
			}
		}
		switch {
		case len(terms) == 0 || n == len(terms):
			all = append(all, scored{ti, n})
		case n > 0:
			partial = append(partial, scored{ti, n})
		}
	}
	best := all
	if len(best) == 0 {
		// Rank by how much of the query matched, then by name for stability, and
		// keep only the top few so a vague query cannot unlock everything.
		sort.Slice(partial, func(i, j int) bool {
			if partial[i].n != partial[j].n {
				return partial[i].n > partial[j].n
			}
			return partial[i].ti.Name < partial[j].ti.Name
		})
		if len(partial) > 4 {
			partial = partial[:4]
		}
		best = partial
	}
	hits := make([]*schema.ToolInfo, 0, len(best))
	for _, s := range best {
		hits = append(hits, s.ti)
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	return hits
}

// hiddenNames lists what is available but not currently shown, so the model can
// be told the set exists without paying for its schemas.
func (g *toolGate) hiddenNames() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, ti := range g.all {
		if ti != nil && !coreTools[ti.Name] && !g.unlocked[ti.Name] {
			out = append(out, ti.Name)
		}
	}
	sort.Strings(out)
	return out
}

// ---- middleware ----

// gateMiddleware narrows the visible tool set on every model call.
type gateMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	gate *toolGate
}

func newGateMiddleware(g *toolGate) *gateMiddleware {
	return &gateMiddleware{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, gate: g}
}

func (m *gateMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if m.gate == nil {
		return ctx, state, nil
	}
	m.gate.remember(state.ToolInfos)
	// The budget guard strips tools entirely on the final turn;;don't put them back.
	if len(state.ToolInfos) == 0 {
		return ctx, state, nil
	}
	state.ToolInfos = m.gate.visible()
	return ctx, state, nil
}

// ---- find_tools ----

// findTools is how the agent reaches the tools that are not shown by default.
type findTools struct{}

func (findTools) Name() string { return findToolsName }
func (findTools) Description() string {
	return "Find and enable a tool that is not currently in your tool list. This workspace has many more tools than the ones you can see — spreadsheets and CSV, PDF and documents, SQL, charts, slides, encoding and hashing, and more. Call this with a short description of what you need (e.g. \"read a pdf\", \"join two csv files\", \"make a chart\") BEFORE concluding that something is impossible or writing a shell workaround. Matching tools become available on your next step."
}
func (findTools) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "what you need to do, in a few words"},
		},
		"required": []string{"query"},
	}
}

func (findTools) Invoke(ctx context.Context, args map[string]any) (string, error) {
	g := toolGateFrom(ctx)
	if g == nil {
		return "工具检索当前不可用。", nil
	}
	query, _ := args["query"].(string)
	hits := g.search(query)
	if len(hits) == 0 {
		hidden := g.hiddenNames()
		if len(hidden) == 0 {
			return "没有更多可用工具:你已经能看到全部工具了。", nil
		}
		return "没有匹配「" + query + "」的工具。当前未展示的工具有:" + strings.Join(hidden, ", ") +
			"。可以用其中一个的名字再检索一次。", nil
	}
	names := make([]string, 0, len(hits))
	var b strings.Builder
	b.WriteString("已启用 " + itoa(len(hits)) + " 个工具,下一步即可直接调用:\n")
	for _, ti := range hits {
		names = append(names, ti.Name)
		b.WriteString("- " + ti.Name + ": " + trunc(ti.Desc, 200) + "\n")
	}
	g.unlock(names)
	return b.String(), nil
}

// withFindTools appends the escape hatch to a tool set.
func withFindTools(tools []agent.BaseTool) []agent.BaseTool {
	return append(tools, findTools{})
}

// ---- context carrier ----

type toolGateKey struct{}

func withToolGate(ctx context.Context, g *toolGate) context.Context {
	if g == nil {
		return ctx
	}
	return context.WithValue(ctx, toolGateKey{}, g)
}

func toolGateFrom(ctx context.Context) *toolGate {
	g, _ := ctx.Value(toolGateKey{}).(*toolGate)
	return g
}

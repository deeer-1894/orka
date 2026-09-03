package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/pathsafe"
)

// eino_context.go — context-window management (P1).
//
// The measured problem: a single pipeline step burned ~58k tokens and three
// steps burned 111k, because big tool outputs (a research report's full text, a
// backtest dump) stay verbatim in history and accumulate. Summarization alone is
// after-the-fact; it can't help a single step whose INPUT is already huge.
//
// Three layers, cheapest first:
//  1. truncation — cap any single oversized tool output, offloading the rest to
//     a file in the user's workspace that the agent can re-read on demand.
//  2. clear      — once history crosses a token budget, replace OLD tool outputs
//     with placeholders (the recent tail is kept intact).
//  3. summarization — existing; the last resort for genuinely long dialogue.
//
// Layers 1 and 2 are tiered rather than destructive, in the sense a context
// database uses the word: what leaves the working set leaves an abstract behind
// and stays addressable on disk.
//
//	L0  a short gist, always in context — enough to decide "do I need this?"
//	L1  the head/tail preview truncation already keeps
//	L2  the full text, in .orka_offload/, reachable with file_read
//
// The layer that was missing is L0 on the clear path. Clearing keeps the tool
// CALL (eino's default handler preserves ToolArgument), so the model could still
// see what it had asked for — but the result side collapsed to a bare pointer,
// which says nothing about whether the answer was three findings or an error.
// That is the shape of the 680-second failure described on subAgentNames below.

const (
	// maxToolOutputChars caps one tool result. Anything longer is offloaded to a
	// file; ~24k chars is roughly 6-8k tokens, enough for a real answer but far
	// below the 58k-token step we measured.
	maxToolOutputChars = 24000
	// clearAboveTokens is the history budget past which old tool outputs are
	// replaced by placeholders.
	clearAboveTokens = 24000
	// offloadDir is where truncated tool output lands inside the user's workspace,
	// so the agent can retrieve it with the file_read tool it already has.
	offloadDir = ".orka_offload"
	// offloadAbstractChars sizes the L0 kept in context for a cleared result.
	// ~260 chars is 60-100 tokens: two orders of magnitude below the result it
	// stands in for, and still enough to carry a conclusion.
	offloadAbstractChars = 260
	// readFileToolName is the tool Orka already exposes for reading a workspace
	// file. It is the only way back to an offloaded result, so the placeholder,
	// the offload path and this name have to agree.
	readFileToolName = "file_read"
)

// protectedToolOutputs are results the reducer must never truncate or clear:
// their output IS the state the next step consumes, so a placeholder breaks it.
func protectedToolOutputs() []string {
	return []string{"validate_factor", "factor_agreement", planToolName}
}

// subAgentNames returns every delegate's tool name.
//
// A sub-agent's result is the MOST expensive output in the system — a whole
// nested agent run, several minutes and tens of thousands of tokens — so
// clearing it as "an old tool result" is exactly backwards. Left unprotected it
// produced the worst run measured here: four researchers returned excellent
// sourced reports in the first 140 seconds, the reducer then replaced them with
// placeholders once the context crossed its threshold, and the orchestrator —
// with nothing left to synthesise — spent the next 680 seconds listing the
// workspace and re-reading unrelated files, ending partial with five plan steps
// unfinished. Delegation looked like the problem; losing its results was.
//
// specs must be the registry the orchestrator was actually BUILT from. Reading
// the built-in list here instead was a silent hole: config.yaml supports a
// custom sub_agents registry, BuildEinoSubAgentTools honours it, and any
// delegate whose name was not one of the built-in seven fell straight back
// through this protection into the failure above.
func subAgentNames(specs []config.SubAgentConfig) []string {
	if len(specs) == 0 {
		specs = DefaultSubAgents() // mirrors BuildEinoSubAgentTools' fallback
	}
	out := make([]string, 0, len(specs))
	for _, sp := range specs {
		if sp.Name != "" {
			out = append(out, sp.Name)
		}
	}
	return out
}

// contextHandlers builds the P1 middleware chain for a run. Order matters:
// patchtoolcalls repairs dangling tool calls first, then reduction trims, then
// the caller's summarization handlers run as the final backstop.
//
// tools are the run's own tools (each gets an L0-preserving clear handler) and
// specs the sub-agent registry the orchestrator was built from.
//
// Best-effort: any middleware that fails to construct is skipped rather than
// blocking the agent — a degraded context policy beats no agent.
func contextHandlers(ctx context.Context, baseStorage, userEmail, label string, tools []agent.BaseTool, specs []config.SubAgentConfig) []adk.ChatModelAgentMiddleware {
	var out []adk.ChatModelAgentMiddleware

	// Bracket the reduction pass so one log line reports both sides of it. Off
	// unless ORKA_CTX_DEBUG=1 (see eino_context_probe.go).
	probePre, probePost := ctxProbePair(label)
	if probePre != nil {
		out = append(out, probePre)
	}

	// Small/fast models occasionally emit a tool_call the history never answers;
	// left dangling it poisons every subsequent request.
	if mw, err := patchtoolcalls.New(ctx, &patchtoolcalls.Config{}); err == nil {
		out = append(out, mw)
	}

	backend := newWorkspaceBackend(baseStorage, userEmail)
	// Never reduce the pipeline's own control tools: their output IS the state
	// that flows to the next step, and a placeholder would break it.
	protected := append(protectedToolOutputs(), subAgentNames(specs)...)

	red, err := reduction.New(ctx, &reduction.Config{
		Backend:           backend,
		ReadFileToolName:  readFileToolName,
		MaxLengthForTrunc: maxToolOutputChars,
		// The path in the placeholder is the only route back to an offloaded
		// result, so it has to be a path file_read can open. eino's defaults name
		// files "/tmp/{trunc,clear}/{call_id}" while workspaceBackend stores them
		// under .orka_offload/ — file_read resolved the advertised path inside the
		// user's root, found nothing, and every offloaded result was unreachable.
		// Generating the path here makes the two agree by construction.
		GenTruncOffloadFilePath: offloadPathFor("trunc"),
		GenClearOffloadFilePath: offloadPathFor("clear"),
		TruncExcludeTools:       protected,
		ClearExcludeTools:       protected,
		MaxTokensForClear:       clearAboveTokens,
		ClearAtLeastTokens:      clearAboveTokens / 3,
		// Per-tool handlers are the only way to override eino's clear placeholder
		// (there is no general hook), so every tool the run owns gets one.
		ToolConfig: l0ClearConfigs(tools, backend),
	})
	if err == nil {
		out = append(out, red)
	}
	if probePost != nil {
		out = append(out, probePost)
	}
	return out
}

// l0ClearConfigs gives each of the run's tools a clear handler that leaves an L0
// abstract behind. Returns nil without a backend: with nowhere to offload to
// there is no L2 to point at, and eino's plain placeholder is the honest answer.
func l0ClearConfigs(tools []agent.BaseTool, backend filesystem.Backend) map[string]*reduction.ToolReductionConfig {
	if backend == nil || len(tools) == 0 {
		return nil
	}
	handler := l0ClearHandler()
	cfgs := make(map[string]*reduction.ToolReductionConfig, len(tools))
	for _, t := range tools {
		if t == nil || t.Name() == "" {
			continue
		}
		// TruncHandler stays nil on purpose: eino falls back to its default for
		// truncation, which already keeps a head+tail preview — an L1 — in context.
		cfgs[t.Name()] = &reduction.ToolReductionConfig{Backend: backend, ClearHandler: handler}
	}
	return cfgs
}

// l0ClearHandler replaces a cleared tool result with an abstract plus a pointer,
// instead of the pointer alone.
func l0ClearHandler() func(context.Context, *reduction.ToolDetail) (*reduction.ClearResult, error) {
	genPath := offloadPathFor("clear")
	return func(ctx context.Context, d *reduction.ToolDetail) (*reduction.ClearResult, error) {
		if d == nil || d.ToolResult == nil {
			return &reduction.ClearResult{NeedClear: false}, nil
		}
		full := joinToolText(d.ToolResult.Parts)
		if strings.TrimSpace(full) == "" {
			return &reduction.ClearResult{NeedClear: false}, nil // nothing to reclaim
		}
		path, err := genPath(ctx, d)
		if err != nil {
			return nil, err
		}
		name := toolNameOf(d)
		placeholder := fmt.Sprintf(
			"<persisted-output>\n[%s] 完整结果(%d 字符)已归档到 %s,用 %s 读取。\n摘要:%s\n</persisted-output>",
			name, len([]rune(full)), path, readFileToolName, l0Abstract(full, offloadAbstractChars))

		return &reduction.ClearResult{
			NeedClear:       true,
			ToolArgument:    d.ToolArgument, // keep the request; only the result is archived
			ToolResult:      &schema.ToolResult{Parts: replaceTextParts(d.ToolResult.Parts, placeholder)},
			NeedOffload:     true,
			OffloadFilePath: path,
			OffloadContent:  offloadBody(name, d, full),
		}, nil
	}
}

// l0Abstract renders the L0 tier: a short gist that stays in context so the model
// can judge whether the archived text is worth fetching back.
//
// The head of the result, not a model-written summary. Clearing happens mid-run
// on the critical path, where an extra LLM call would buy latency exactly when
// the context is already straining — and the head is well aimed, because these
// agents are prompted to answer conclusion-first (the researcher is told to
// return "a short conclusion first, then key points"), so a result's opening
// lines ARE its finding.
func l0Abstract(text string, limit int) string {
	fields := strings.Fields(text) // collapses newlines and runs of spaces
	if len(fields) == 0 {
		return ""
	}
	return trunc(strings.Join(fields, " "), limit)
}

// offloadBody is the L2 tier: the full text, headed by the same abstract the
// model keeps plus the request that produced it — so a file found later with
// file_list explains itself without the conversation that created it.
func offloadBody(name string, d *reduction.ToolDetail, full string) string {
	var b strings.Builder
	b.WriteString("# " + name + "\n")
	if d.ToolArgument != nil && strings.TrimSpace(d.ToolArgument.Text) != "" {
		b.WriteString("# 请求:" + l0Abstract(d.ToolArgument.Text, 300) + "\n")
	}
	b.WriteString("# 归档于 " + time.Now().UTC().Format(time.RFC3339) + "\n\n")
	b.WriteString(full)
	return b.String()
}

// offloadPathFor names an archive after the tool that produced it, inside the one
// directory the agent can list. An opaque call id told the model nothing about
// what a file held; "researcher-clear-a1b2c3d4.txt" makes file_list on
// .orka_offload/ readable as an index of the run's archived work.
func offloadPathFor(kind string) func(context.Context, *reduction.ToolDetail) (string, error) {
	return func(_ context.Context, d *reduction.ToolDetail) (string, error) {
		id := ""
		if d != nil && d.ToolContext != nil {
			id = d.ToolContext.CallID
		}
		if id == "" {
			// Call ids are optional; a unique name still beats overwriting a sibling.
			id = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		return filepath.Join(offloadDir, fileSegment(toolNameOf(d))+"-"+kind+"-"+fileSegment(id)+".txt"), nil
	}
}

func toolNameOf(d *reduction.ToolDetail) string {
	if d != nil && d.ToolContext != nil && d.ToolContext.Name != "" {
		return d.ToolContext.Name
	}
	return "tool"
}

// fileSegment reduces a tool name or call id to one safe filename segment. Both
// are provider- or model-supplied and end up as paths.
func fileSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "x"
	}
	if len(out) > 48 { // a hard cut, not trunc: its ellipsis is not a filename char
		out = out[:48]
	}
	return out
}

func joinToolText(parts []schema.ToolOutputPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == schema.ToolPartTypeText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// replaceTextParts collapses a result's text to the placeholder, keeping any
// non-text parts. The placeholder lands once rather than being copied into every
// text part, which would bill the abstract several times over.
func replaceTextParts(parts []schema.ToolOutputPart, placeholder string) []schema.ToolOutputPart {
	out := make([]schema.ToolOutputPart, 0, len(parts))
	replaced := false
	for _, p := range parts {
		if p.Type == schema.ToolPartTypeText {
			if replaced {
				continue
			}
			p.Text = placeholder
			replaced = true
		}
		out = append(out, p)
	}
	return out
}

// subAgentContextHandlers builds the reduction chain for ONE delegate.
//
// Delegates had none. The orchestrator's middlewares stop at the orchestrator —
// BuildEinoSubAgentTools installs only a budget guard — so a researcher's own
// retrieval accumulated verbatim and unbounded, which is where the volume
// actually is: fetch_url averages 3,024 characters here and http_request 14,039,
// and a delegate told to make "~6, at most ~10" calls was measured making 15.
// Nothing trimmed that; only subAgentMaxTokens eventually cut the delegate off,
// mid-work, which is the expensive way to discover a context is too big.
//
// Thresholds are the orchestrator's, deliberately. The pressure being managed is
// the model's context window, which does not care which agent filled it, and
// ClearAtLeastTokens already stops a clear that would not reclaim enough to be
// worth breaking the prompt cache — so an ordinary short delegation is never
// touched, and only the over-researching one is.
//
// Clearing a delegate's retrieval is safe in a way it was not before this file
// learned to leave an abstract behind: eino preserves the tool ARGUMENTS, so a
// cited source URL survives in the fetch_url call that fetched it, and the L0
// keeps the head of what came back. The delegate can still cite; it just has to
// re-read for detail.
//
// tools must be the delegate's OWN scoped set — l0ClearConfigs is keyed by tool
// name, and a delegate only ever calls what it was given.
func subAgentContextHandlers(ctx context.Context, name string, tools []agent.BaseTool) []adk.ChatModelAgentMiddleware {
	base := offloadRootFrom(ctx)
	if base == "" {
		return nil // no storage configured → nowhere to offload; leave the delegate as-is
	}
	// nil specs: a delegate has no delegates of its own, so there is nothing
	// sub-agent-shaped in its tool set to protect.
	return contextHandlers(ctx, base, agent.MetaFrom(ctx).UserEmail, name, tools, nil)
}

// runUserEmail resolves whose workspace this run writes to, so offloaded tool
// output lands in the right storage root.
func runUserEmail(rc *agent.RunContext) string {
	if rc == nil || rc.Ctx == nil {
		return ""
	}
	return agent.MetaFrom(rc.Ctx).UserEmail
}

// --- offload root carrier -----------------------------------------------------

// Sub-agents are constructed deep inside BuildEinoOrchestrator, which has no
// access to storage config. Carrying the base path on the context — the same way
// the run budget, tool gate and plan tracker already reach middlewares — keeps
// two exported builders from growing a storage parameter they otherwise have no
// use for.
type offloadRootKey struct{}

func withOffloadRoot(ctx context.Context, base string) context.Context {
	if base == "" {
		return ctx
	}
	return context.WithValue(ctx, offloadRootKey{}, base)
}

func offloadRootFrom(ctx context.Context) string {
	s, _ := ctx.Value(offloadRootKey{}).(string)
	return s
}

// --- workspace-backed offload store -----------------------------------------

// workspaceBackend implements the eino filesystem.Backend over the caller's own
// workspace, so offloaded tool output lands in a real file the agent can read
// back with file_read (an in-memory backend would tell the model to read a file
// that does not exist for any of Orka's tools).
//
// Only Read/Write/LsInfo are meaningful for offloading; the search/edit methods
// are not used by the reduction middleware and report that plainly.
type workspaceBackend struct{ root string }

func newWorkspaceBackend(baseStorage, userEmail string) filesystem.Backend {
	if baseStorage == "" {
		return nil // no storage configured → truncation offload disabled
	}
	return &workspaceBackend{root: pathsafe.UserRoot(baseStorage, userEmail)}
}

// resolve keeps every path inside the user's workspace root (pathsafe rejects
// traversal), so an offload path can never escape the caller's storage.
func (w *workspaceBackend) resolve(p string) (string, error) {
	return pathsafe.Resolve(w.root, strings.TrimPrefix(filepath.Clean("/"+p), "/"))
}

// offloadRel keeps writes inside the offload directory while preserving a path
// that is already there, so what the model was told to read is what gets
// written. Paths from elsewhere (a future eino default, another caller) are
// still corralled by name rather than trusted.
func offloadRel(p string) string {
	clean := strings.TrimPrefix(filepath.Clean("/"+p), "/")
	if clean == offloadDir || strings.HasPrefix(clean, offloadDir+string(filepath.Separator)) {
		return clean
	}
	return filepath.Join(offloadDir, filepath.Base(clean))
}

func (w *workspaceBackend) Write(_ context.Context, req *filesystem.WriteRequest) error {
	if req == nil {
		return fmt.Errorf("nil write request")
	}
	p, err := w.resolve(offloadRel(req.FilePath))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(req.Content), 0o644)
}

func (w *workspaceBackend) Read(_ context.Context, req *filesystem.ReadRequest) (*filesystem.FileContent, error) {
	if req == nil {
		return nil, fmt.Errorf("nil read request")
	}
	p, err := w.resolve(req.FilePath)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	from := req.Offset - 1
	if from < 0 {
		from = 0
	}
	if from > len(lines) {
		from = len(lines)
	}
	to := len(lines)
	if req.Limit > 0 && from+req.Limit < to {
		to = from + req.Limit
	}
	return &filesystem.FileContent{Content: strings.Join(lines[from:to], "\n")}, nil
}

func (w *workspaceBackend) LsInfo(_ context.Context, req *filesystem.LsInfoRequest) ([]filesystem.FileInfo, error) {
	p, err := w.resolve(req.Path)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]filesystem.FileInfo, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, filesystem.FileInfo{
			Path:       filepath.Join(req.Path, e.Name()),
			Size:       info.Size(),
			ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

var errBackendUnsupported = fmt.Errorf("workspace offload backend supports read/write/ls only")

func (w *workspaceBackend) GrepRaw(context.Context, *filesystem.GrepRequest) ([]filesystem.GrepMatch, error) {
	return nil, errBackendUnsupported
}
func (w *workspaceBackend) GlobInfo(context.Context, *filesystem.GlobInfoRequest) ([]filesystem.FileInfo, error) {
	return nil, errBackendUnsupported
}
func (w *workspaceBackend) Edit(context.Context, *filesystem.EditRequest) error {
	return errBackendUnsupported
}

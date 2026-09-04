package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// Per-RUN subdirectories under it (see offloadDirFor): the archive is scratch
	// for the run that produced it, and pooling every run's scratch in one
	// directory turned it into a corpus. It reached 303 files, cost ~3,900 tokens
	// every time file_list touched it, and an orchestrator that found it spent 40
	// of its 52 shell calls grepping earlier runs' fetches — archaeology on its
	// own leftovers instead of research.
	offloadDir = ".orka_offload"
	// offloadAbstractChars sizes the L0 kept in context for a cleared result.
	//
	// It was 260 chars on the theory that a result's opening lines ARE its
	// finding. That held for sub-agent reports, which are prompted to answer
	// conclusion-first. It does not hold for the raw tool output this actually
	// clears now that MULTI_AGENT=0, and the three placeholders measured on a
	// 178-call research run show where all 260 chars went:
	//
	//   'URL: https://…/middleware_toolreduction/ Title: Reduction | CloudWeGo
	//    Reduction | CloudWeGo MaxLengthForTrunc? │ │ Yes → Truncate content…'
	//   'URL: https://…/middleware_summarization/ Title: Summarization | CloudWeGo
	//    … DocumentationKitex Hertz Volo EinoAboutBlogCooperationEnglish中文 Light'
	//   '# file_read # 请求:{"path": ".orka_offload/…/file_read-clear-call_3jrj…"}
	//    # 归档于 … # file_read # 请求:{"path": ".orka_offload/…"} # 归档于 …'
	//
	// A 120-char URL already carried by ToolArgument, site navigation, and — for
	// a re-read — nothing but nested archive headers. None of it says what the
	// page CONTAINED, so the model had no basis to decide and read the file back:
	// 101 of 178 tool calls were archive re-reads, 24 of them repeats of a path
	// already read, one file fetched back five times.
	//
	// l0Digest drops those three classes and this budget buys prose instead. The
	// arithmetic that matters is per-read: ~180 extra tokens held in context
	// against a ~5,000-token re-read avoided, so a placeholder pays for itself
	// the first time it prevents one.
	offloadAbstractChars = 700
	// readFileToolName is the tool Orka already exposes for reading a workspace
	// file. It is the only way back to an offloaded result, so the placeholder,
	// the offload path and this name have to agree.
	readFileToolName = "file_read"
	// argClearAboveChars is the argument size past which a cleared tool call
	// keeps a pointer instead of its payload. Below it, the argument IS the
	// request — a URL, a path, a query — and is worth its tokens.
	//
	// Clearing only ever touched tool RESULTS, on the assumption that the result
	// is the big half. For retrieval that holds. For anything that WRITES it is
	// backwards, and a code task measured here shows by how much: across 26 tool
	// calls the arguments carried 53,959 chars against 16,116 of results, and
	// file_write alone was 46,774 of the arguments while its results — "ok" —
	// came to 630. Context climbed 23k → 45k and the clear pass never ran once,
	// because the only pool it could see was the 4.6k of results, short of
	// ClearAtLeastTokens, and eino skips the whole pass when it cannot meet that.
	argClearAboveChars = 1200
	// argDigestChars sizes the gist left behind for a cleared argument. Smaller
	// than offloadAbstractChars: an argument's identity is usually its first
	// line, and the file it names can be read in full.
	argDigestChars = 300
	// clearFloorTokens is how much the clear pass must be able to release before
	// eino will run it at all — it skips the ENTIRE pass otherwise, so the floor
	// has to sit below one maximal clearable item or items pile up underneath it
	// forever. It did not: at clearAboveTokens/3 the floor was 8,000 while
	// maxToolOutputChars caps a single result at 24,000 chars (~6,800 tokens), so
	// no lone result could ever trip it, and a code run sat at 45k context with
	// the pass declining to run 16 cycles running.
	//
	// Not zero: its purpose is real, and measured here — rewriting history
	// mid-conversation drops this provider's prefix-cache hit rate from 98% to
	// 63%, so a pass that reclaims a trickle costs more than it saves.
	clearFloorTokens = 4000
)

// durableArgPayload names, per tool, the argument field holding a payload that
// the call itself puts on disk, and the field saying where it landed.
//
// This is the case worth special-casing: file_write's content is at its path the
// moment the call returns, so the history can point at the real file rather than
// archive a second copy. It also stays correct if the agent rewrote the file —
// the pointer resolves to what is there NOW, which is what a later read wants.
var durableArgPayload = map[string]struct{ payload, location string }{
	"file_write": {payload: "content", location: "path"},
}

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
		GenTruncOffloadFilePath: offloadPathFor(ctx, "trunc"),
		GenClearOffloadFilePath: offloadPathFor(ctx, "clear"),
		TruncExcludeTools:       protected,
		ClearExcludeTools:       protected,
		MaxTokensForClear:       clearAboveTokens,
		ClearAtLeastTokens:      clearFloorTokens,
		// Per-tool handlers are the only way to override eino's clear placeholder
		// (there is no general hook), so every tool the run owns gets one.
		ToolConfig: l0ClearConfigs(ctx, tools, backend),
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
func l0ClearConfigs(buildCtx context.Context, tools []agent.BaseTool, backend filesystem.Backend) map[string]*reduction.ToolReductionConfig {
	if backend == nil || len(tools) == 0 {
		return nil
	}
	handler := l0ClearHandler(buildCtx)
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
func l0ClearHandler(buildCtx context.Context) func(context.Context, *reduction.ToolDetail) (*reduction.ClearResult, error) {
	genPath := offloadPathFor(buildCtx, "clear")
	return func(ctx context.Context, d *reduction.ToolDetail) (*reduction.ClearResult, error) {
		if d == nil || d.ToolResult == nil {
			return &reduction.ClearResult{NeedClear: false}, nil
		}
		full := joinToolText(d.ToolResult.Parts)
		if strings.TrimSpace(full) == "" {
			return &reduction.ClearResult{NeedClear: false}, nil // nothing to reclaim
		}
		name := toolNameOf(d)

		// A result that IS an archive read back must not become a second archive.
		//
		// It did, and the loop closed on itself: clearing evicts a fetch to a file,
		// the model reads the file back, the read's own result crosses the
		// threshold and is archived to a NEW file, and so on. Measured over one
		// run: of 101 archive re-reads, 65 targeted files whose original content
		// was itself a file_read result — three and four levels deep, each level
		// prepending its own header until the L0 budget held nothing but nesting
		// metadata. The archive directory reached 11MB of the same pages.
		//
		// Pointing back at the source instead breaks the chain and costs nothing:
		// the bytes are already on disk at src. It also makes the placeholder
		// STABLE across cycles — no new call-id filename each time — which stops
		// this path from invalidating the provider's prefix cache on every pass.
		if src := archivedSource(d); src != "" {
			return &reduction.ClearResult{
				NeedClear:    true,
				ToolArgument: d.ToolArgument,
				ToolResult: &schema.ToolResult{Parts: replaceTextParts(d.ToolResult.Parts, fmt.Sprintf(
					"<persisted-output>\n[%s] 这是归档内容的回读,未重复归档。原件仍在 %s,用 %s 读取。\n摘要:%s\n</persisted-output>",
					name, src, readFileToolName, l0Digest(full, offloadAbstractChars)))},
				NeedOffload: false,
			}, nil
		}

		path, err := genPath(ctx, d)
		if err != nil {
			return nil, err
		}
		placeholder := fmt.Sprintf(
			"<persisted-output>\n[%s] 完整结果(%d 字符)已归档到 %s,用 %s 读取。\n摘要:%s\n</persisted-output>",
			name, len([]rune(full)), path, readFileToolName, l0Digest(full, offloadAbstractChars))

		// Shrink the argument too. eino writes ToolArgument back into the history
		// and then measures what the pass released by re-counting the whole edited
		// message list, so this counts toward ClearAtLeastTokens — which is what
		// lets the pass run at all on a write-heavy run.
		arg, carried := clearedToolArgument(name, d.ToolArgument, path)

		return &reduction.ClearResult{
			NeedClear:       true,
			ToolArgument:    arg,
			ToolResult:      &schema.ToolResult{Parts: replaceTextParts(d.ToolResult.Parts, placeholder)},
			NeedOffload:     true,
			OffloadFilePath: path,
			OffloadContent:  offloadBody(name, d, full, carried),
		}, nil
	}
}

// clearedToolArgument shrinks a cleared tool call's argument, returning the
// replacement and any payload the offload file must carry so nothing is lost.
//
// Two shapes. A tool whose payload the call itself puts on disk (file_write)
// needs no archive at all — the history points at the path it wrote, and
// carried is empty. Anything else large rides along in the SAME offload file as
// the result, because ClearResult offers one file per call and a request and
// its answer belong together anyway.
//
// A small argument is returned untouched: it is the request, and a run that
// cannot see what it asked for is worse off than one paying 40 tokens to know.
func clearedToolArgument(name string, arg *schema.ToolArgument, offloadPath string) (*schema.ToolArgument, string) {
	if arg == nil || len(arg.Text) <= argClearAboveChars {
		return arg, ""
	}
	if spec, ok := durableArgPayload[name]; ok {
		if shrunk, ok := pointArgAtItsOwnFile(arg.Text, spec.payload, spec.location); ok {
			return &schema.ToolArgument{Text: shrunk}, ""
		}
	}
	return &schema.ToolArgument{Text: fmt.Sprintf(
		"<persisted-arg>[%s] 完整参数(%d 字符)见 %s 的「请求」段,用 %s 读取。摘要:%s</persisted-arg>",
		name, len([]rune(arg.Text)), offloadPath, readFileToolName,
		l0Abstract(arg.Text, argDigestChars))}, arg.Text
}

// pointArgAtItsOwnFile replaces the payload field with a note naming the path
// the same call wrote it to, leaving every other field intact so the call still
// reads as the request it was.
//
// Reports false rather than guessing when the argument is not the expected
// object — a model that sent something unusual keeps its argument verbatim.
func pointArgAtItsOwnFile(argText, payloadField, locationField string) (string, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(argText), &obj); err != nil {
		return "", false
	}
	payload, ok := obj[payloadField].(string)
	if !ok || len(payload) <= argClearAboveChars {
		return "", false
	}
	location, ok := obj[locationField].(string)
	if !ok || location == "" {
		return "", false
	}
	obj[payloadField] = fmt.Sprintf("<persisted-arg>已写入 %s(%d 字符),用 %s 读取当前内容</persisted-arg>",
		location, len([]rune(payload)), readFileToolName)
	shrunk, err := json.Marshal(obj)
	if err != nil {
		return "", false
	}
	return string(shrunk), true
}

// l0Abstract renders a plain head-of-text gist: whitespace collapsed, truncated.
//
// Still the right shape for text that is already a conclusion — a sub-agent
// report, a tool argument — and the fallback when l0Digest's filters reject
// everything. It is NOT the right shape for raw page text; see l0Digest.
func l0Abstract(text string, limit int) string {
	fields := strings.Fields(text) // collapses newlines and runs of spaces
	if len(fields) == 0 {
		return ""
	}
	return trunc(strings.Join(fields, " "), limit)
}

// l0Digest renders the L0 tier: a gist dense enough that the model can decide
// whether the archived text is worth fetching back WITHOUT fetching it back.
//
// Line structure is the signal, so this works line by line rather than
// collapsing the whole result first the way l0Abstract does. Three classes of
// line are measured noise and get dropped (see offloadAbstractChars for the
// placeholders that motivated each):
//
//   - archive headers, which is all a re-read of an archive contains at the top
//   - the URL echo, ~120 chars already present in the ToolArgument kept alongside
//   - site navigation: short, non-CJK, no sentence punctuation
//
// Box-drawing runes are stripped rather than used to reject a line, because the
// diagrams in these docs carry real text between the glyphs ("Yes → Truncate
// content, save full content to Backend").
//
// A markdown heading is kept even though it would fail the navigation test: an
// outline is the densest possible description of a document. Pages fetched
// through fetch_url arrive flattened and rarely have any, so this mostly matters
// for archived file_read of real markdown.
func l0Digest(text string, limit int) string {
	var kept []string
	n := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.Join(strings.Fields(stripBoxDrawing(raw)), " ")
		if line == "" || isArchiveHeader(line) || strings.HasPrefix(line, "URL: ") {
			continue
		}
		if !isMarkdownHeading(line) && isNavigationChrome(line) {
			continue
		}
		if n > 0 {
			n++ // the space strings.Join will insert before this line
		}
		room := limit - n
		if room <= 0 {
			break
		}
		if len([]rune(line)) > room {
			// trunc appends an ellipsis, so it returns n+1 runes; spend one of the
			// remaining runes on it rather than overrunning the budget.
			kept = append(kept, trunc(line, room-1))
			break
		}
		kept = append(kept, line)
		n += len([]rune(line))
	}
	if len(kept) == 0 {
		// Every line was filtered — a diagram, a blob, a format not anticipated
		// here. A head-of-text gist beats an empty placeholder.
		return l0Abstract(text, limit)
	}
	return strings.Join(kept, " ")
}

// isArchiveHeader matches the lines offloadBody writes. They appear inside a
// tool RESULT only when that result is an archive being read back, where they
// are pure nesting metadata.
//
// The tool-name line ("# fetch_url") is matched on shape rather than on a
// registry, because the tool set is per-run. Requiring a snake_case identifier
// keeps it off ordinary markdown headings: a first draft tested only for
// "single word" and swallowed "# Reduction" — the heading of the very page this
// digest exists to describe. A document heading that happens to be one
// lowercase word is still lost; that is the residual cost of not knowing the
// tool list here.
var toolNameHeadingRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func isArchiveHeader(line string) bool {
	rest := strings.TrimPrefix(line, "# ")
	if rest == line {
		return false
	}
	return strings.HasPrefix(rest, "请求:") || strings.HasPrefix(rest, "归档于 ") ||
		toolNameHeadingRe.MatchString(rest)
}

func isMarkdownHeading(line string) bool {
	h := strings.TrimLeft(line, "#")
	return h != line && strings.HasPrefix(h, " ")
}

// isNavigationChrome rejects menu items and other short label lines. A line long
// enough, or carrying CJK, or punctuated like a sentence, is treated as content.
func isNavigationChrome(line string) bool {
	if len([]rune(line)) >= 24 || strings.ContainsAny(line, ".:。，、；:") {
		return false
	}
	for _, r := range line {
		if r >= 0x4E00 && r <= 0x9FFF { // CJK unified ideographs
			return false
		}
	}
	return true
}

// stripBoxDrawing removes the Unicode Box Drawing block, which these docs use
// for ASCII diagrams whose text is worth keeping.
func stripBoxDrawing(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x2500 && r <= 0x257F {
			return ' '
		}
		return r
	}, s)
}

// offloadPathRe finds an archive path inside a tool's arguments. Deliberately
// not JSON-aware: file_read passes {"path": …}, but shell and python reach the
// same files through a command line or a script body, and a run measured here
// spent 28 of its 30 shell/python calls grepping the archive.
var offloadPathRe = regexp.MustCompile(regexp.QuoteMeta(offloadDir) + `/[\w./-]+`)

// archivedSource returns the archive path a tool result was itself read FROM,
// or "" when the result is original. See the call site in l0ClearHandler.
func archivedSource(d *reduction.ToolDetail) string {
	if d == nil || d.ToolArgument == nil {
		return ""
	}
	return offloadPathRe.FindString(d.ToolArgument.Text)
}

// offloadBody is the L2 tier: the full text, headed by the same abstract the
// model keeps plus the request that produced it — so a file found later with
// file_list explains itself without the conversation that created it.
// carriedArg, when non-empty, is an argument too large to keep in context whose
// only remaining copy is this file — so it is written out whole rather than as
// the one-line gist that suffices when the argument itself survives in history.
func offloadBody(name string, d *reduction.ToolDetail, full, carriedArg string) string {
	var b strings.Builder
	b.WriteString("# " + name + "\n")
	if carriedArg != "" {
		b.WriteString("# 请求(完整):\n" + carriedArg + "\n")
	} else if d.ToolArgument != nil && strings.TrimSpace(d.ToolArgument.Text) != "" {
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
func offloadPathFor(buildCtx context.Context, kind string) func(context.Context, *reduction.ToolDetail) (string, error) {
	dir := offloadDirFor(buildCtx)
	return func(_ context.Context, d *reduction.ToolDetail) (string, error) {
		id := ""
		if d != nil && d.ToolContext != nil {
			id = d.ToolContext.CallID
		}
		if id == "" {
			// Call ids are optional; a unique name still beats overwriting a sibling.
			id = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		return filepath.Join(dir, fileSegment(toolNameOf(d))+"-"+kind+"-"+fileSegment(id)+".txt"), nil
	}
}

// offloadDirFor scopes a run's archive to its own subdirectory. Pooled, the
// archive is a growing corpus of every run's fetches that happens to sit in the
// agent's workspace — and an agent that finds one will mine it. Scoped, it holds
// only what this run itself offloaded, which is the only thing the placeholders
// point at anyway.
//
// Falls back to the flat directory when no run id is on the context (tests, the
// quant pipeline), which is the previous behaviour rather than a broken path.
func offloadDirFor(ctx context.Context) string {
	if id := runIDFrom(ctx); id != "" {
		return filepath.Join(offloadDir, fileSegment(id))
	}
	return offloadDir
}

type runIDKey struct{}

// withRunID carries the run's record id for archive scoping. Same carrier as the
// budget, tool gate and offload root — the run's identity has to reach code that
// runs deep inside the agent graph.
func withRunID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDKey{}, id)
}

func runIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(runIDKey{}).(string)
	return s
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

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/reduction"

	"github.com/orka-oss/orka_core/agent"
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
)

// contextHandlers builds the P1 middleware chain for a run. Order matters:
// patchtoolcalls repairs dangling tool calls first, then reduction trims, then
// the caller's summarization handlers run as the final backstop.
//
// Best-effort: any middleware that fails to construct is skipped rather than
// blocking the agent — a degraded context policy beats no agent.
func contextHandlers(ctx context.Context, baseStorage, userEmail string) []adk.ChatModelAgentMiddleware {
	var out []adk.ChatModelAgentMiddleware

	// Small/fast models occasionally emit a tool_call the history never answers;
	// left dangling it poisons every subsequent request.
	if mw, err := patchtoolcalls.New(ctx, &patchtoolcalls.Config{}); err == nil {
		out = append(out, mw)
	}

	red, err := reduction.New(ctx, &reduction.Config{
		Backend:           newWorkspaceBackend(baseStorage, userEmail),
		ReadFileToolName:  "file_read", // the tool Orka already exposes
		MaxLengthForTrunc: maxToolOutputChars,
		// Never truncate the pipeline's own control tools: their output IS the
		// state that flows to the next step, and a placeholder would break it.
		TruncExcludeTools:  []string{"validate_factor", "factor_agreement", planToolName},
		ClearExcludeTools:  []string{"validate_factor", "factor_agreement", planToolName},
		MaxTokensForClear:  clearAboveTokens,
		ClearAtLeastTokens: clearAboveTokens / 3,
	})
	if err == nil {
		out = append(out, red)
	}
	return out
}

// runUserEmail resolves whose workspace this run writes to, so offloaded tool
// output lands in the right storage root.
func runUserEmail(rc *agent.RunContext) string {
	if rc == nil || rc.Ctx == nil {
		return ""
	}
	return agent.MetaFrom(rc.Ctx).UserEmail
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

func (w *workspaceBackend) Write(_ context.Context, req *filesystem.WriteRequest) error {
	if req == nil {
		return fmt.Errorf("nil write request")
	}
	// Offloads carry tool-call ids as paths; keep them under one tidy directory.
	p, err := w.resolve(filepath.Join(offloadDir, filepath.Base(req.FilePath)))
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

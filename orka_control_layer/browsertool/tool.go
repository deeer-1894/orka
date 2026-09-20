package browsertool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/agent"
)

type tool struct {
	engine      *Engine
	baseStorage string
}

// New constructs immutable metadata and a model-free operation tool. It never
// opens a browser or creates a workspace; absent configuration stays unavailable.
func New(dialer connectors.BrowserDialer, baseStorage string) agent.BaseTool {
	t := &tool{baseStorage: baseStorage}
	if dialer != nil {
		t.engine = NewEngine(dialer)
	}
	return t
}
func (*tool) Name() string  { return "browser" }
func (*tool) Group() string { return "browser" }
func (*tool) Description() string {
	return "Observe and operate the shared browser page through bounded DOM snapshots, unique CSS selectors or snapshot refs. For generated local pages, use action=preview with the existing conversation-relative HTML path; the service transfers the actual file without copying HTML into model messages. Preview supports self-contained UTF-8 HTML up to 1 MiB, inline JS/CSS and data images; external resources, fetch, relative asset files and server-backed apps are unavailable in this offline preview. It returns a source-file hash and page snapshot, not proof that all interactions work. Then use click/fill/select/evaluate/screenshot to verify behavior, or GUI on the same page. open accepts HTTP(S), never file://. The code sandbox has separate filesystem/network namespaces: do not guess absolute paths, start local servers or manually paste page chunks to cross that boundary. On preview failure inspect the named file/limit once; preserve the failure and report unsupported requirements instead of repeating the same workaround. No extra model is called. Refs remain valid for unchanged elements in the same document/run/GUI epoch; use a new snapshot after stale_ref. Use fill_form for ordered fields without submitting; partial failures report completed fields. Actions wait briefly for an available target before dispatch, never replay uncertain mutations. Default action receipts contain changed text and current controls; snapshot or view=full returns full bounded text. Read actions, ready_state, change and progress before deciding the next step. Same-origin frames support frame selector paths; cross-origin frames and pixel-only content require GUI on this same page, then a fresh DOM snapshot. Actions report observations, not verified business success."
}
func (*tool) Schema() map[string]any { return toolSchema() }
func (t *tool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	req, err := parseRequest(args)
	if err != nil {
		return encodeToolResult(Result{Action: req.Action, Error: NewActionError("invalid_arguments", err.Error())})
	}
	if t.engine == nil {
		return encodeToolResult(Result{Action: req.Action, Error: NewActionError("unavailable", "browser service is not configured; no action was performed")})
	}
	identity, err := connectors.GUIIdentityFromContext(ctx)
	if err != nil {
		return encodeToolResult(Result{Action: req.Action, Error: NewActionError("identity_required", "browser requires trusted owner, conversation and run identity")})
	}
	result, err := t.engine.Run(ctx, identity, req, NewFiles(t.baseStorage, identity))
	if err != nil {
		result.OK = false
		if result.Error == nil {
			var actionErr *ActionError
			if errors.As(err, &actionErr) {
				result.Error = actionErr
			} else {
				result.Error = NewActionError("outcome_unknown", "browser operation could not be confirmed; inspect the page before retrying")
			}
		}
	}
	result.Action = req.Action
	return encodeToolResult(result)
}
func encodeToolResult(result Result) (string, error) {
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > MaxSnapshotBytes {
		raw, _ = json.Marshal(Result{Action: result.Action, PageID: result.PageID, PageEpoch: result.PageEpoch, Error: NewActionError("output_limit", "browser result exceeded its output bound; prior actions were not rolled back")})
	}
	return string(raw), nil
}

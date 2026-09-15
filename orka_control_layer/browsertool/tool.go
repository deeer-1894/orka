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
	return "Observe and operate the shared browser page through bounded DOM snapshots, unique CSS selectors or snapshot refs. Supports page JavaScript, screenshots and downloads; no extra model is called. Frames/pixel-only content may require GUI. Actions report browser observations, not verified business success."
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

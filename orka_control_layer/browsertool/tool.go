package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/agent"
)

type tool struct {
	engine      *Engine
	baseStorage string
	navigation  *navigationRetryGuard
}

// New constructs immutable metadata and a model-free operation tool. It never
// opens a browser or creates a workspace; absent configuration stays unavailable.
func New(dialer connectors.BrowserDialer, baseStorage string) agent.BaseTool {
	t := &tool{baseStorage: baseStorage, navigation: newNavigationRetryGuard()}
	if dialer != nil {
		t.engine = NewEngine(dialer)
	}
	return t
}
func (*tool) Name() string  { return "browser" }
func (*tool) Group() string { return "browser" }
func (*tool) Description() string {
	return "Observe and operate the shared browser page through bounded DOM snapshots, unique CSS selectors or snapshot refs. " +
		"To read a linked page, action=open its absolute href instead of clicking and snapshotting. open accepts HTTP(S) only, never file://. " +
		"For a generated local page use action=preview with its conversation-relative .html path (self-contained UTF-8 up to 1 MiB, inline JS/CSS; no external resources or servers); it proves the page loads, not that it works — then click/fill/evaluate/screenshot to check behavior. " +
		"The code sandbox is a separate machine: do not guess absolute paths or start local servers to reach it. " +
		"Every successful action returns the latest observation (changed text and current controls): act on it instead of taking another snapshot by habit; use snapshot or view=full only when it is missing or stale, and wait for a specific condition. " +
		"Refs stay valid for unchanged elements; after stale_ref take a new snapshot. observation_failed means observe again, never repeat the click or submit. " +
		"fill_form fills ordered fields without submitting. Same-origin iframes take a frame selector path; cross-origin frames and canvas need GUI on this page. " +
		"When the next in-page steps are known (fill, click, wait, screenshot), send them as one action=batch with steps. " +
		"Results are observations, not proof of business success; on failure report what is unsupported instead of repeating a workaround."
}

func (*tool) Schema() map[string]any { return toolSchema() }
func (t *tool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	if action, _ := args["action"].(string); action == batchAction {
		return encodeToolResult(t.runBatch(ctx, args))
	}
	req, err := parseRequest(args)
	if err != nil {
		return encodeToolResult(Result{Action: req.Action, Error: NewActionError("invalid_arguments", err.Error())})
	}
	return encodeToolResult(t.run(ctx, req))
}

// run executes one validated action with the full per-action contract:
// identity, navigation guard, observation recovery and error mapping.
func (t *tool) run(ctx context.Context, req Request) Result {
	if t.engine == nil {
		return Result{Action: req.Action, Error: NewActionError("unavailable", "browser service is not configured; no action was performed")}
	}
	req.observation = observationLimitsFrom(ctx)
	identity, err := connectors.GUIIdentityFromContext(ctx)
	if err != nil {
		return Result{Action: req.Action, Error: NewActionError("identity_required", "browser requires trusted owner, conversation and run identity")}
	}
	if blocked := t.navigation.blocked(identity, req); blocked != nil {
		return Result{Action: req.Action, Error: blocked}
	}
	result, err := t.engine.Run(ctx, identity, req, NewFiles(t.baseStorage, identity))
	if shouldRecoverObservation(req, result, err) {
		recoveryCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		observed, observeErr := t.engine.Run(recoveryCtx, identity, Request{Action: "snapshot", View: "full", TimeoutMS: 4000, observation: req.observation}, NewFiles(t.baseStorage, identity))
		cancel()
		if observeErr == nil && observed.OK && observed.Snapshot != nil {
			observed.Action = req.Action
			observed.ElapsedMS += result.ElapsedMS
			observed.Form, observed.Files, observed.Preview, observed.Value = result.Form, result.Files, result.Preview, result.Value
			observed.Recovery = &Recovery{From: "observation_failed", ActionAcknowledged: true}
			result, err = observed, nil
		}
	}
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
	t.navigation.record(identity, req, result)
	return result
}

func shouldRecoverObservation(req Request, result Result, err error) bool {
	if err == nil || result.Error == nil || result.Error.Code != "observation_failed" {
		return false
	}
	switch req.Action {
	case "open", "preview", "click", "fill", "select", "press", "scroll", "fill_form":
		return true
	default:
		return false
	}
}
func encodeToolResult(result Result) (string, error) {
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > MaxSnapshotBytes {
		raw, _ = json.Marshal(Result{Action: result.Action, PageID: result.PageID, PageEpoch: result.PageEpoch, Error: NewActionError("output_limit", "browser result exceeded its output bound; prior actions were not rolled back")})
	}
	return string(raw), nil
}

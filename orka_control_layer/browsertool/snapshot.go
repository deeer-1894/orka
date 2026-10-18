package browsertool

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/orka-oss/orka_control_layer/connectors"
)

//go:embed scripts/snapshot.js
var snapshotScript string

//go:embed scripts/evaluate.js
var evaluateScript string

//go:embed scripts/dom.js
var domScript string

//go:embed scripts/observation.js
var observationScript string

type pageReply struct {
	Change   *PageChange   `json:"change"`
	Progress *PageProgress `json:"progress"`
	OK       bool          `json:"ok"`
	URL      string        `json:"url"`
	Title    string        `json:"title"`
	Snapshot *Snapshot     `json:"snapshot"`
	Value    any           `json:"value"`
	Ready    bool          `json:"ready"`
	X        float64       `json:"x"`
	Y        float64       `json:"y"`
	Error    *ActionError  `json:"error"`
}

type runtimeReply struct {
	Result struct {
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails json.RawMessage `json:"exceptionDetails"`
}

func createWorld(ctx context.Context, lease connectors.BrowserLease) (int64, error) {
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := lease.Execute(ctx, "Page.getFrameTree", map[string]any{}, &tree); err != nil {
		return 0, err
	}
	if tree.FrameTree.Frame.ID == "" {
		return 0, NewActionError("browser_error", "Main browser frame is unavailable.")
	}
	var world struct {
		ID int64 `json:"executionContextId"`
	}
	err := lease.Execute(ctx, "Page.createIsolatedWorld", map[string]any{"frameId": tree.FrameTree.Frame.ID, "worldName": "orka-browser"}, &world)
	if err == nil && world.ID == 0 {
		err = NewActionError("browser_error", "Isolated browser context is unavailable.")
	}

	return world.ID, err
}

func page(ctx context.Context, s Session, operation string, req Request) (pageReply, error) {
	limits := req.observation.normalized()
	payload := map[string]any{"action": req.Action, "view": req.View, "frame": req.Frame, "ref": req.Ref, "snapshot_id": req.SnapshotID, "selector": req.Selector, "text": req.Text, "value": req.Value, "condition": req.Condition, "url": req.URL, "direction": req.Direction, "amount": req.Amount, "text_limit": limits.textChars, "element_limit": limits.elementRefs, "byte_limit": limits.outputBytes}
	if operation == "snapshot" {
		delete(payload, "text")
		delete(payload, "value")
	}
	// The server executes fixed helpers in a protected world. Generic evaluate
	// cannot redefine the globals or prototypes used by an observation.
	method := "Orka.act"
	if operation == "snapshot" || operation == "wait" || operation == "commit_observation" {
		method = "Orka.observe"
	}
	var response runtimeReply
	err := s.Lease.Execute(ctx, method, map[string]any{"operation": operation, "request": payload}, &response)
	if err != nil {
		return pageReply{}, err
	}
	return decodeReply(response)
}

func decodeReply(response runtimeReply) (pageReply, error) {
	if len(response.ExceptionDetails) > 0 && string(response.ExceptionDetails) != "null" {
		return pageReply{}, NewActionError("script_error", "Browser page script failed.")
	}
	if len(response.Result.Value) > MaxEvaluationBytes {
		return pageReply{}, NewActionError("output_limit", "Browser result exceeds its size limit.")
	}
	var result pageReply
	if err := json.Unmarshal(response.Result.Value, &result); err != nil {
		return result, NewActionError("script_error", "Browser script returned an invalid result.")
	}
	if !result.OK {
		if result.Error != nil {
			return result, result.Error
		}
		return result, NewActionError("script_error", "Browser page script failed.")
	}
	return result, nil
}

func observe(ctx context.Context, s Session, result *Result, request ...Request) error {
	req := Request{Action: "snapshot"}
	if len(request) > 0 {
		req = request[0]
	}
	reply, err := page(ctx, s, "snapshot", req)
	if err != nil {
		return err
	}
	if reply.Snapshot == nil {
		return NewActionError("browser_error", "Browser did not return a snapshot.")
	}
	result.URL = reply.URL
	result.Title = reply.Title
	result.Snapshot = reply.Snapshot
	result.Change = reply.Change
	result.Progress = reply.Progress
	return nil
}

func evaluate(ctx context.Context, s Session, source string) (any, error) {
	var response runtimeReply
	err := s.Lease.Execute(ctx, "Runtime.callFunctionOn", map[string]any{"executionContextId": s.ContextID, "functionDeclaration": evaluateScript, "arguments": []any{map[string]any{"value": source}}, "returnByValue": true, "awaitPromise": true}, &response)
	if err != nil {
		return nil, err
	}
	reply, err := decodeReply(response)
	return reply.Value, err
}

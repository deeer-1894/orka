package browsertool

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"

	"github.com/orka-oss/orka_control_layer/connectors"
)

//go:embed scripts/snapshot.js
var snapshotScript string

//go:embed scripts/evaluate.js
var evaluateScript string

type pageReply struct {
	OK       bool         `json:"ok"`
	URL      string       `json:"url"`
	Title    string       `json:"title"`
	Snapshot *Snapshot    `json:"snapshot"`
	Value    any          `json:"value"`
	Ready    bool         `json:"ready"`
	X        float64      `json:"x"`
	Y        float64      `json:"y"`
	Error    *ActionError `json:"error"`
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
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return pageReply{}, err
	}
	info := s.Lease.Info()
	payload := map[string]any{"operation": operation, "scope": []any{s.Identity.OwnerID, s.Identity.ConversationID, s.Identity.RunID, info.PageID, info.PageEpoch}, "nonce": hex.EncodeToString(nonce), "ref": req.Ref, "snapshot_id": req.SnapshotID, "selector": req.Selector, "text": req.Text, "value": req.Value, "condition": req.Condition, "url": req.URL, "direction": req.Direction, "amount": req.Amount}
	raw, err := json.Marshal(payload)
	if err != nil {
		return pageReply{}, err
	}
	// Keep user-controlled strings out of generated source. Snapshot's small
	// payload uses evaluate for a single initial observation; other operations
	// pass arguments separately so a full-sized input is not truncated by JS.
	var response runtimeReply
	if operation == "snapshot" {
		expression := "(" + snapshotScript + ")(" + string(raw) + ")"
		if len(expression) > MaxExpressionBytes {
			return pageReply{}, NewActionError("output_limit", "Browser helper exceeds the script limit.")
		}
		err = s.Lease.Execute(ctx, "Runtime.evaluate", map[string]any{"contextId": s.ContextID, "expression": expression, "returnByValue": true, "awaitPromise": true}, &response)
	} else {
		err = s.Lease.Execute(ctx, "Runtime.callFunctionOn", map[string]any{"executionContextId": s.ContextID, "functionDeclaration": snapshotScript, "arguments": []any{map[string]any{"value": payload}}, "returnByValue": true, "awaitPromise": true}, &response)
	}
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

func observe(ctx context.Context, s Session, result *Result) error {
	reply, err := page(ctx, s, "snapshot", Request{})
	if err != nil {
		return err
	}
	if reply.Snapshot == nil {
		return NewActionError("browser_error", "Browser did not return a snapshot.")
	}
	result.URL = reply.URL
	result.Title = reply.Title
	result.Snapshot = reply.Snapshot
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

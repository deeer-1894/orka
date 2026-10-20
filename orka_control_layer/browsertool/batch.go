package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	batchAction   = "batch"
	maxBatchSteps = 8
)

// Navigation stays one call per page so the research session's host circuit
// breaker and source capture see every open; batches cover in-page sequences.
var batchableActions = map[string]bool{
	"snapshot": true, "click": true, "fill": true, "fill_form": true, "select": true,
	"press": true, "scroll": true, "wait": true, "evaluate": true, "screenshot": true,
}

// runBatch validates every step before executing any, then runs them in order
// through the single-action path and stops at the first failure. Completed
// steps are never replayed. The result is the last executed step's observation.
func (t *tool) runBatch(ctx context.Context, args map[string]any) Result {
	steps, err := parseBatch(args)
	if err != nil {
		return Result{Action: batchAction, Error: NewActionError("invalid_arguments", err.Error())}
	}
	var last Result
	summaries := make([]StepResult, 0, len(steps))
	for _, step := range steps {
		if ctx.Err() != nil {
			break
		}
		last = t.run(ctx, step)
		summaries = append(summaries, StepResult{Action: last.Action, OK: last.OK, Value: last.Value, ElapsedMS: last.ElapsedMS, Error: last.Error})
		if !last.OK {
			break
		}
	}
	if len(summaries) < len(steps) && last.OK {
		last.OK = false
		last.Error = NewActionError("canceled", fmt.Sprintf("batch stopped after %d of %d steps; completed steps were not replayed", len(summaries), len(steps)))
	}
	last.Action = batchAction
	last.Steps = summaries
	return last
}

func parseBatch(args map[string]any) ([]Request, error) {
	for name := range args {
		if name != "action" && name != "steps" && name != "snapshot_id" {
			return nil, fmt.Errorf("argument %s is not supported for batch", safeArgumentName(name))
		}
	}
	shared, _ := args["snapshot_id"].(string)
	if _, ok := args["snapshot_id"]; ok && shared == "" {
		return nil, errors.New("batch snapshot_id must be a nonempty string")
	}
	raw, err := json.Marshal(args["steps"])
	if err != nil || len(raw) > 64<<10 {
		return nil, errors.New("steps must contain bounded JSON values")
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > maxBatchSteps {
		return nil, fmt.Errorf("batch requires 1 to %d steps", maxBatchSteps)
	}
	steps := make([]Request, 0, len(items))
	for i, item := range items {
		action, _ := item["action"].(string)
		if _, hasRef := item["ref"]; hasRef && shared != "" {
			if _, own := item["snapshot_id"]; !own {
				item["snapshot_id"] = shared
			}
		}
		if !batchableActions[action] {
			return nil, fmt.Errorf("step %d: action %q cannot run in a batch; call open, preview and download on their own", i+1, action)
		}
		req, err := parseRequest(item)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		steps = append(steps, req)
	}
	return steps, nil
}

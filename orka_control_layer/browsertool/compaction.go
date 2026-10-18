package browsertool

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed scripts/compact_snapshot.js
var compactSnapshotScript string

// compactObservation enriches the model-facing copy of a stable observation.
// The fixed observer remains the sole owner of refs and delta baselines.
func compactObservation(ctx context.Context, session *Session, snapshot *Snapshot, limits observationLimits) error {
	if snapshot == nil || !needsObservationCompaction(snapshot, limits) {
		return nil
	}
	// Compaction enriches a receipt that the fixed observer already validated.
	// Any optional enrichment failure must preserve that receipt.
	if session.ContextID == 0 {
		if err := createCurrentWorld(ctx, session); err != nil {
			return nil
		}
	}
	budget := limits.outputBytes - 1024
	if budget < 4096 {
		budget = 4096
	}
	var response runtimeReply
	err := session.Lease.Execute(ctx, "Runtime.callFunctionOn", map[string]any{
		"executionContextId":  session.ContextID,
		"functionDeclaration": compactSnapshotScript,
		"arguments": []any{map[string]any{"value": map[string]any{
			"snapshot":   snapshot,
			"byte_limit": budget,
		}}},
		"returnByValue": true,
	}, &response)
	if err != nil {
		return nil
	}
	if len(response.ExceptionDetails) > 0 && string(response.ExceptionDetails) != "null" {
		return nil
	}
	if len(response.Result.Value) > MaxEvaluationBytes {
		return nil
	}
	var compacted Snapshot
	if err := json.Unmarshal(response.Result.Value, &compacted); err != nil {
		return nil
	}
	if compacted.ID == "" || compacted.ID != snapshot.ID {
		return nil
	}
	*snapshot = compacted
	return nil
}

func needsObservationCompaction(snapshot *Snapshot, limits observationLimits) bool {
	for _, element := range snapshot.Elements {
		if element.Tag == "a" && element.Role == "link" {
			return true
		}
	}
	if snapshot.Mode == "full" {
		interactive := make(map[string]struct{}, len(snapshot.Elements))
		for _, element := range snapshot.Elements {
			if element.Name != "" && (element.Tag == "a" || element.Tag == "button" || element.Tag == "summary") {
				interactive[element.Name] = struct{}{}
			}
		}
		previous := ""
		for _, line := range strings.Split(snapshot.Text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && (line == previous || hasObservationName(interactive, line)) {
				return true
			}
			if line != "" {
				previous = line
			}
		}
	}
	raw, err := json.Marshal(snapshot)
	return err == nil && len(raw) > limits.outputBytes-1024
}

func hasObservationName(names map[string]struct{}, line string) bool {
	_, ok := names[line]
	return ok
}

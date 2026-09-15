package connectors

import "encoding/hex"

// Model observations remain a separate bounded result, never action receipts.
func guiTaskMemory(frame map[string]any) map[string]any {
	memory, ok := frame["task_memory"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := memory["goals"].([]any)
	if !ok {
		return nil
	}
	omitted := 0
	if count := guiToken(memory["omitted_goals"]); count != nil {
		omitted = *count
	}
	if len(raw) > 8 {
		omitted += len(raw) - 8
		raw = raw[len(raw)-8:]
	}
	goals := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		goal, ok := item.(map[string]any)
		if !ok || goal["source"] != "model_observation" {
			continue
		}
		label, labelOK := goal["goal"].(string)
		observation, observationOK := goal["observation"].(string)
		status, statusOK := goal["status"].(string)
		if !labelOK || label == "" || !observationOK || !statusOK || (status != "pending" && status != "complete" && status != "blocked") {
			continue
		}
		entry := map[string]any{"goal": guiClip(label, 96), "status": status, "observation": guiClip(observation, 1800), "source": "model_observation"}
		for _, key := range []string{"observed_step", "observation_seq"} {
			if n := guiToken(goal[key]); n != nil {
				entry[key] = *n
			}
		}
		if digest, ok := goal["screenshot_sha256"].(string); ok && len(digest) == 64 {
			if _, err := hex.DecodeString(digest); err == nil {
				entry["screenshot_sha256"] = digest
			}
		}
		goals = append(goals, entry)
	}
	return map[string]any{"goals": goals, "omitted_goals": omitted,
		"note": "Model-reported task progress, not acceptance evidence or operator receipts. Source steps refer to pre-action observations; historical marks are not current targets."}
}

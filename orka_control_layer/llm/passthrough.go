package llm

import (
	"encoding/json"
	"fmt"
)

// passthrough.go — provider fields we do not model.
//
// Reasoning control is the case that forced this. There is no agreed field for
// it: endpoints that are otherwise OpenAI-compatible each spell it differently,
// and one deployment here serves nine models across three conventions
// (reasoning_effort, thinking, enable_thinking). Modelling one of them would be
// picking a side, and modelling all of them would be a schema that goes stale
// the next time a provider ships.
//
// It matters because it is the only lever that actually shortens thinking.
// max_tokens caps OUTPUT, and a reasoning model spends its reasoning tokens
// first — so capping output truncates the answer after the thinking has already
// been paid for, which is precisely how a 693-second run ended mid-tag having
// called no tools and written nothing.

// protectedFields are the request's own. A passthrough exists to add provider
// options, not to rewrite what is being asked — letting a config value replace
// `messages` or `model` would turn a tuning knob into a way to silently send a
// different request than the one the agent built.
var protectedFields = map[string]bool{
	"model": true, "messages": true, "tools": true,
	"stream": true, "stream_options": true,
}

// withExtra marshals v and merges extra into the resulting JSON object,
// skipping any protected key. Returns v's own encoding unchanged when there is
// nothing to merge, so the common path pays nothing.
func withExtra(v any, extra map[string]any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil || len(extra) == 0 {
		return body, err
	}
	var merged map[string]any
	if err := json.Unmarshal(body, &merged); err != nil {
		return nil, fmt.Errorf("passthrough: re-decode request: %w", err)
	}
	for k, val := range extra {
		if protectedFields[k] {
			continue
		}
		merged[k] = val
	}
	return json.Marshal(merged)
}

// reasoningFor resolves the passthrough for one model: its own entry when it
// has one, the global default otherwise. Per-model because a single endpoint
// commonly fronts models that disagree about the field name.
func reasoningFor(model string, global map[string]any, byModel map[string]map[string]any) map[string]any {
	if m, ok := byModel[model]; ok {
		return m
	}
	return global
}

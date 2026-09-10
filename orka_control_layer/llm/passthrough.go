package llm

import (
	"context"
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

// Thinking is a caller's INTENT about reasoning, as opposed to the provider
// field that expresses it. The two are separate on purpose: which field a model
// understands is a property of the endpoint (glm takes `thinking`, OpenAI takes
// `reasoning_effort`, Qwen takes `enable_thinking`, and this deployment's mini
// model appears to take none of them), while whether the user wants deep
// thinking for THIS task is a property of the request.
//
// Keeping them apart is what lets the UI offer a switch at all. A switch that
// sent the provider field would silently stop working the moment someone picked
// a different model from the same dropdown — the exact failure that took an
// afternoon of A/B measurement to notice.
const (
	ThinkingDefault = ""    // whatever the deployment configured
	ThinkingOn      = "on"  // let the model think: send no reduction
	ThinkingOff     = "off" // send the configured reduction for this model
)

type thinkingKey struct{}

// WithThinking records the caller's intent for calls made under ctx.
func WithThinking(ctx context.Context, intent string) context.Context {
	if intent == "" {
		return ctx
	}
	return context.WithValue(ctx, thinkingKey{}, intent)
}

func thinkingFrom(ctx context.Context) string {
	v, _ := ctx.Value(thinkingKey{}).(string)
	return v
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

// ThinkingIntentForTest exposes the intent stored on a context. Exported only
// so another package's tests can assert what a middleware decided; the intent
// itself is read by this package's client.
func ThinkingIntentForTest(ctx context.Context) string { return thinkingFrom(ctx) }

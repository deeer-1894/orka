package llm

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// Each of these is a real provider's spelling of the same idea. The passthrough
// exists because there is no agreed one, and because a single endpoint here
// fronts models that disagree.
func TestEveryProviderSpellingSurvivesToTheWire(t *testing.T) {
	cases := map[string]map[string]any{
		"openai":     {"reasoning_effort": "low"},
		"ark/doubao": {"thinking": map[string]any{"type": "disabled"}},
		"openrouter": {"reasoning": map[string]any{"effort": "low", "max_tokens": 1024}},
		"qwen/vllm":  {"enable_thinking": false},
	}
	for name, extra := range cases {
		body, err := withExtra(wireRequest{Model: "m"}, extra)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := decode(t, body)
		for k := range extra {
			if _, ok := got[k]; !ok {
				t.Errorf("%s: %q never reached the wire", name, k)
			}
		}
	}
}

// A tuning knob must not become a way to send a different request than the one
// the agent built. Overwriting `messages` would do exactly that.
func TestThePassthroughCannotRewriteTheRequest(t *testing.T) {
	wr := wireRequest{Model: "real-model", Messages: []wireReqMessage{{Role: "user", Content: "the real question"}}}
	body, err := withExtra(wr, map[string]any{
		"model":          "someone-elses-model",
		"messages":       []any{},
		"tools":          []any{},
		"stream":         true,
		"stream_options": map[string]any{},
		"thinking":       map[string]any{"type": "disabled"},
	})
	if err != nil {
		t.Fatalf("withExtra: %v", err)
	}
	got := decode(t, body)
	if got["model"] != "real-model" {
		t.Errorf("model was overwritten: %v", got["model"])
	}
	if ms, _ := got["messages"].([]any); len(ms) != 1 {
		t.Errorf("messages were overwritten: %v", got["messages"])
	}
	if got["stream"] == true {
		t.Error("stream was flipped by a passthrough")
	}
	// The option it was actually for still gets through.
	if _, ok := got["thinking"]; !ok {
		t.Error("the reasoning option was dropped along with the protected ones")
	}
}

// Nothing configured must change nothing — this runs on every request.
func TestNoPassthroughLeavesTheBodyAlone(t *testing.T) {
	wr := wireRequest{Model: "m", Temperature: 0.5}
	plain, _ := json.Marshal(wr)
	for _, extra := range []map[string]any{nil, {}} {
		got, err := withExtra(wr, extra)
		if err != nil {
			t.Fatalf("withExtra: %v", err)
		}
		if string(got) != string(plain) {
			t.Fatalf("body changed with no passthrough:\n got %s\nwant %s", got, plain)
		}
	}
}

// One endpoint, nine models, three conventions: the per-model entry has to win,
// and a model without one has to fall back rather than get nothing.
func TestPerModelOverridesTheGlobalSetting(t *testing.T) {
	global := map[string]any{"reasoning_effort": "low"}
	byModel := map[string]map[string]any{
		"doubao-seed-2.1-turbo": {"thinking": map[string]any{"type": "disabled"}},
	}
	if got := reasoningFor("doubao-seed-2.1-turbo", global, byModel); got["thinking"] == nil {
		t.Errorf("the model's own setting was not used: %v", got)
	}
	if got := reasoningFor("kimi-k3", global, byModel); got["reasoning_effort"] != "low" {
		t.Errorf("a model with no entry did not fall back to the global setting: %v", got)
	}
	if got := reasoningFor("anything", nil, nil); got != nil {
		t.Errorf("nothing configured produced %v", got)
	}
}

// Package modelprofile carries the selected connection across execution adapters.
// Credentials are available only to trusted in-process adapters, never JSON/logs.
package modelprofile

import (
	"context"
	"encoding/json"
)

const OpenAICompatible = "openai-compatible"

// Capabilities records successful probes, not advertised or inferred support.
// False includes unverified; it must not trigger a different account's fallback.
type Capabilities struct {
	Text   bool `json:"text"`
	Vision bool `json:"vision"`
	Tools  bool `json:"tools"`
}

// Snapshot is a value frozen at run entry. Do not persist APIKey or put it in
// browser payloads. Trusted GUI transports must explicitly construct their wire
// credentials; ordinary JSON serialization intentionally omits them.
// CallPolicy contains explicitly configured limits/optional provider parameters.
// Zero fields use deployment-independent defaults; no model-name inference.
type CallPolicy struct {
	FirstMaxTokens  int    `json:"first_max_tokens,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type Snapshot struct {
	Policy       CallPolicy   `json:"policy"`
	ProfileID    string       `json:"profile_id"`
	Protocol     string       `json:"protocol"`
	BaseURL      string       `json:"base_url"`
	Model        string       `json:"model"`
	APIKey       string       `json:"-"`
	Capabilities Capabilities `json:"capabilities"`
}

func (s Snapshot) String() string   { b, _ := json.Marshal(s); return string(b) }
func (s Snapshot) GoString() string { return s.String() }

type contextKey struct{}

func WithContext(ctx context.Context, snapshot Snapshot) context.Context {
	return context.WithValue(ctx, contextKey{}, snapshot)
}
func FromContext(ctx context.Context) (Snapshot, bool) {
	if ctx == nil {
		return Snapshot{}, false
	}
	s, ok := ctx.Value(contextKey{}).(Snapshot)
	return s, ok
}

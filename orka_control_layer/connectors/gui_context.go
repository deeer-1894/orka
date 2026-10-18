package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/modelprofile"
)

// GUIIdentity is supplied by the authenticated run lifecycle, never tool args.
// RunID identifies this execution; OwnerID+ConversationID owns browser state.
type GUIIdentity struct {
	OwnerID        string `json:"owner_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
}
type guiIdentityKey struct{}

func WithGUIIdentity(ctx context.Context, identity GUIIdentity) context.Context {
	return context.WithValue(ctx, guiIdentityKey{}, identity)
}

// GUIModelConfig is the transport boundary mapped from the selected snapshot.
// Ordinary formatting/JSON cannot expose the API key; only guiWireConfig can.
type GUIModelConfig struct {
	BaseURL        string                  `json:"base_url"`
	APIKey         string                  `json:"-"`
	Model          string                  `json:"model"`
	VisionVerified bool                    `json:"vision_verified"`
	Policy         modelprofile.CallPolicy `json:"policy"`
}

func (c GUIModelConfig) String() string   { b, _ := json.Marshal(c); return string(b) }
func (c GUIModelConfig) GoString() string { return c.String() }

// GUIIdentityFromContext extracts trusted run identity without requiring a model.
func GUIIdentityFromContext(ctx context.Context) (GUIIdentity, error) {
	meta := agent.MetaFrom(ctx)
	identity := GUIIdentity{meta.UserEmail, meta.ConversationID, meta.RunID}
	if identity == (GUIIdentity{}) {
		identity, _ = ctx.Value(guiIdentityKey{}).(GUIIdentity)
	}
	for _, value := range []string{identity.OwnerID, identity.ConversationID, identity.RunID} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return identity, fmt.Errorf("GUI requires trusted owner/conversation/run identity")
		}
	}
	return identity, nil
}

func guiContext(ctx context.Context) (GUIIdentity, GUIModelConfig, error) {
	identity, err := GUIIdentityFromContext(ctx)
	if err != nil {
		return identity, GUIModelConfig{}, err
	}
	selected, ok := modelprofile.FromContext(ctx)
	if !ok || selected.Protocol != modelprofile.OpenAICompatible {
		return identity, GUIModelConfig{}, fmt.Errorf("GUI requires an explicit OpenAI-compatible selected model snapshot; no global fallback")
	}
	policy, err := guiPolicy(selected.Policy)
	if err != nil {
		return identity, GUIModelConfig{}, err
	}
	config := GUIModelConfig{BaseURL: selected.BaseURL, APIKey: selected.APIKey, Model: selected.Model, VisionVerified: selected.Capabilities.Vision, Policy: policy}
	endpoint, err := url.Parse(config.BaseURL)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.TrimSpace(config.Model) == "" {
		return identity, GUIModelConfig{}, fmt.Errorf("GUI selected model configuration is invalid")
	}
	return identity, config, nil
}

func guiWireConfig(config GUIModelConfig) map[string]any {
	// Called only immediately before an authenticated private-network send.
	return map[string]any{"base_url": config.BaseURL, "api_key": config.APIKey,
		"model": config.Model, "vision_verified": config.VisionVerified, "policy": config.Policy}
}

// GUI limits are executor capabilities, independent of a provider/model name.
// Normalize once: the wire and reservation use the same effective policy.
func guiPolicy(p modelprofile.CallPolicy) (modelprofile.CallPolicy, error) {
	if p.FirstMaxTokens < 0 || p.MaxTokens < 0 || p.TimeoutSeconds < 0 || p.FirstMaxTokens > 1<<20 || p.MaxTokens > 1<<20 || p.TimeoutSeconds > 7200 || p.MaxTokens > 0 && p.FirstMaxTokens > p.MaxTokens {
		return p, fmt.Errorf("invalid GUI model policy limits")
	}
	switch p.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return p, fmt.Errorf("invalid GUI model reasoning policy")
	}
	if p.MaxTokens == 0 || p.MaxTokens > 4096 {
		p.MaxTokens = 4096
	}
	if p.FirstMaxTokens == 0 || p.FirstMaxTokens > p.MaxTokens {
		p.FirstMaxTokens = p.MaxTokens
	}
	if p.TimeoutSeconds == 0 || p.TimeoutSeconds > 45 {
		p.TimeoutSeconds = 45
	}
	return p, nil
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/modelsettings"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/modelprofile"
)

// modelSnapshot contains no locks or registry. It is captured once at run entry
// and inherited by delegates, routing, auxiliary calls and recovery execution.
// ChatService itself is never copied: cancellation/confirmation stay shared.
type modelSnapshot struct {
	profile    string
	cfg        config.LLMConfig
	client     llm.Client
	connection modelprofile.Snapshot
	verified   map[string]modelsettings.Verification
	policies   map[string]modelprofile.CallPolicy
}

type modelSnapshotKey struct{}

func (s *ChatService) defaultModels() modelSnapshot {
	legacy := s.Cfg.LLM
	cfg := config.LLMConfig{OpenAIBaseURL: legacy.OpenAIBaseURL, Model: legacy.Model, Models: legacy.Models, MaxRetries: legacy.MaxRetries}
	cfg.Models = modelsettings.OrderedModels(cfg.Model, cfg.Models)
	cfg.Model = ""
	if len(cfg.Models) > 0 {
		cfg.Model = cfg.Models[0]
	}
	cfg.OpenAIAPIKey = "" // credentials belong only to clients, not config snapshots
	return modelSnapshot{cfg: cfg, client: s.Client, connection: modelprofile.Snapshot{ProfileID: "deployment", Protocol: modelprofile.OpenAICompatible, BaseURL: cfg.OpenAIBaseURL, Model: cfg.Model, APIKey: s.Cfg.LLM.OpenAIAPIKey}}
}

func (s *ChatService) modelsForContext(ctx context.Context) modelSnapshot {
	if ctx != nil {
		if m, ok := ctx.Value(modelSnapshotKey{}).(modelSnapshot); ok {
			return m
		}
	}
	return s.defaultModels()
}

// Shared transport pools connections only. Credentials are request headers and
// never stored on the transport; each snapshot owns its client and key.
var userModelTransport = llm.NewOpenAIClient("", "").HTTP.Transport

func (s *ChatService) resolveModels(owner string) (modelSnapshot, error) {
	m := s.defaultModels()
	m.profile = modelProfile(owner, modelsettings.Config{Provider: "deployment", BaseURL: m.cfg.OpenAIBaseURL, APIKey: s.Cfg.LLM.OpenAIAPIKey, Models: m.cfg.Models})
	if s.ModelSettings == nil || owner == "" {
		return m, nil
	}
	saved, exists, err := s.ModelSettings.Get(owner)
	if err != nil {
		return modelSnapshot{}, err
	}
	if !exists || !saved.Enabled {
		return m, nil
	}
	if err := modelsettings.RequireSupportedProtocol(saved.Protocol); err != nil {
		return modelSnapshot{}, err
	}
	m.connection = modelprofile.Snapshot{ProfileID: saved.ID, Protocol: saved.Protocol, BaseURL: saved.BaseURL, APIKey: saved.APIKey}
	m.verified = saved.Verified
	m.policies = saved.Policies
	m.profile = modelProfile(owner, saved)
	first := saved.Models[0] // enabled settings are validated to contain a model
	m.cfg = config.LLMConfig{OpenAIBaseURL: saved.BaseURL, Model: first, VLMModel: first, Models: append([]string(nil), saved.Models...), MaxRetries: m.cfg.MaxRetries}
	// Connection identity excludes model order/selection: the same credential
	// still shares its provider concurrency allowance after a list-only edit.
	identity := modelProfile(owner, modelsettings.Config{Protocol: saved.Protocol, BaseURL: saved.BaseURL, APIKey: saved.APIKey})
	retries := m.cfg.MaxRetries
	m.client = &pooledModelClient{pool: &s.modelClients, identity: identity, create: func() llm.Client {
		raw := llm.NewOpenAIClient(saved.BaseURL, saved.APIKey)
		raw.HTTP.Transport = userModelTransport
		raw.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		safe := &privateModelClient{client: raw, key: saved.APIKey}
		metered := llm.NewMetered(llm.NewAccounted(safe), func(ctx context.Context) string { return agent.MetaFrom(ctx).AgentID })
		return llm.NewLimiterFromEnv(llm.NewRetry(metered, llm.RetryConfig{MaxAttempts: retries}))
	}}
	return m, nil
}

// Hash private connection material without returning or persisting its key.
// The owner prevents a profile token being reused across different accounts.
func modelProfile(owner string, c modelsettings.Config) string {
	b, _ := json.Marshal(struct {
		Owner  string
		Config modelsettings.Config
		Key    string
	}{owner, c, c.APIKey})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *ChatService) withUserModels(ctx context.Context, owner string) (context.Context, error) {
	if _, ok := ctx.Value(modelSnapshotKey{}).(modelSnapshot); ok {
		return ctx, nil
	}
	m, err := s.resolveModels(owner)
	if err != nil {
		return ctx, err
	}
	return s.withSelectedModel(context.WithValue(ctx, modelSnapshotKey{}, m), ModelAuto), nil
}

// ModelConfigForUser supplies the same effective configuration as execution.
// The returned configuration contains no key.
func (s *ChatService) ModelConfigForUser(owner string) (config.LLMConfig, error) {
	m, err := s.resolveModels(owner)
	return m.cfg, err
}

func (m modelSnapshot) modelFor(version string) (llm.Client, string) {
	if version != "" && version != ModelAuto && m.cfg.AllowsModel(version) {
		return m.client, version
	}
	return m.client, m.cfg.Model
}

var ErrModelSelection = errors.New("所选模型不在当前模型列表中，请重新选择模型后再试。")

func (m modelSnapshot) validateSelection(version string) error {
	if version != "" && version != ModelAuto && !m.cfg.AllowsModel(version) {
		return ErrModelSelection
	}
	return nil
}

// Freeze the explicit choice for every call in this run; delegates and
// auxiliaries inherit the same model without role-based routing.
func (s *ChatService) withSelectedModel(ctx context.Context, version string) context.Context {
	m := s.modelsForContext(ctx)
	_, name := m.modelFor(version)
	m.cfg.Model, m.cfg.VLMModel = name, name
	m.connection.Model = name
	m.connection.Policy = m.policies[name]
	v := m.verified[name]
	m.connection.Capabilities = modelprofile.Capabilities{Text: v.Text.Verified, Vision: v.Vision.Verified, Tools: v.Tools.Verified}
	return modelprofile.WithContext(context.WithValue(ctx, modelSnapshotKey{}, m), m.connection)
}

// Provider failures may echo the Authorization header. Redact before retries,
// logging, checkpointing or user-visible error conversion. HTTP error bodies
// are discarded because JSON/URL-encoded credentials defeat literal replacement.
// Retain HTTP status
// and cancellation identity for existing retry/failure policies.
type privateModelClient struct {
	client *llm.OpenAIClient
	key    string
}

func (c *privateModelClient) safeError(err error) error {
	if err == nil || c.key == "" {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		return &llm.APIError{Status: apiErr.Status, Body: "provider request failed"}
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.key, "[redacted]"))
}
func (c *privateModelClient) Chat(ctx context.Context, r llm.Request) (llm.Response, error) {
	resp, err := c.client.Chat(ctx, r)
	return resp, c.safeError(err)
}
func (c *privateModelClient) ChatStream(ctx context.Context, r llm.Request, f func(string)) (llm.Response, error) {
	resp, err := c.client.ChatStream(ctx, r, f)
	return resp, c.safeError(err)
}

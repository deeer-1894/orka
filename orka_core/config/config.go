// Package config loads layered configuration: config.yaml as a base, then
// environment variables override individual values. No secrets are hardcoded.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultDevSecret is the placeholder secret shipped in config.yaml. It is
// rejected at startup outside dev mode (see Validate) so a deployment can never
// silently run with a publicly-known signing key.
const DefaultDevSecret = "dev-only-change-me"

// Config is the full application configuration.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	LLM      LLMConfig      `yaml:"llm"`
	Storage  StorageConfig  `yaml:"storage"`
	Agent    AgentConfig    `yaml:"agent"`
	Security SecurityConfig `yaml:"security"`
	Obs      ObsConfig      `yaml:"obs"`
}

type ServerConfig struct {
	ControlAddr      string   `yaml:"control_addr"`
	ToolsAddr        string   `yaml:"tools_addr"`
	CORSAllowedHosts []string `yaml:"cors_allowed_hosts"`
}

type LLMConfig struct {
	OpenAIBaseURL string `yaml:"openai_base_url"`
	OpenAIAPIKey  string `yaml:"openai_api_key"`
	Model         string `yaml:"model"`
	MiniModel     string `yaml:"mini_model"`
	VLMModel      string `yaml:"vlm_model"`
}

type StorageConfig struct {
	MongoURI        string `yaml:"mongo_uri"`
	MongoDB         string `yaml:"mongo_db"`
	RedisAddr       string `yaml:"redis_addr"`
	BaseStoragePath string `yaml:"base_storage_path"`
}

type AgentConfig struct {
	RunMode          string `yaml:"run_mode"`
	CheckpointTTLSec int    `yaml:"checkpoint_ttl_sec"`
	GUIAgentWSURL    string `yaml:"gui_agent_ws_url"`
}

type SecurityConfig struct {
	CtxTokenSecret string `yaml:"ctx_token_secret"`
	CtxTokenTTLSec int    `yaml:"ctx_token_ttl_sec"`
}

type ObsConfig struct {
	LogLevel        string  `yaml:"log_level"`
	PersistSampling float64 `yaml:"persist_sampling"`
}

// IsDev reports whether the process is running in dev mode (ORKA_DEV=1), which
// relaxes the secret check below.
func IsDev() bool {
	return os.Getenv("ORKA_DEV") == "1"
}

// Validate enforces security-critical invariants at startup. Outside dev mode a
// missing or placeholder HMAC secret is fatal: with a known signing key an
// attacker could forge context tokens (bypassing tool RBAC) and user sessions.
func (c *Config) Validate() error {
	s := c.Security.CtxTokenSecret
	if IsDev() {
		return nil
	}
	if s == "" || s == DefaultDevSecret {
		return errors.New("security.ctx_token_secret is empty or the dev placeholder; " +
			"set a strong CTX_TOKEN_SECRET (or run with ORKA_DEV=1 for local dev)")
	}
	if len(s) < 16 {
		return errors.New("security.ctx_token_secret is too short; use >= 16 random bytes")
	}
	return nil
}

// CtxSecret is the key for control<->tools context tokens (must match the
// tools_server). SessionSecret is a distinct key derived for user session
// tokens, so a forged context token can never be replayed as a session (and
// vice versa) — defence in depth even when one base secret is configured.
func (c *Config) CtxSecret() []byte { return []byte(c.Security.CtxTokenSecret) }

func (c *Config) SessionSecret() string {
	sum := sha256.Sum256([]byte(c.Security.CtxTokenSecret + "|orka-session-v1"))
	return hex.EncodeToString(sum[:])
}

// Load reads path (if present) then applies env overrides and defaults.
func Load(path string) (*Config, error) {
	var c Config
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, &c); err != nil {
				return nil, fmt.Errorf("config parse %q: %w", path, err)
			}
		case os.IsNotExist(err):
			// fall through to env + defaults
		default:
			return nil, fmt.Errorf("config read %q: %w", path, err)
		}
	}
	c.applyEnv()
	c.applyDefaults()
	return &c, nil
}

func (c *Config) applyEnv() {
	envStr(&c.Server.ControlAddr, "CONTROL_ADDR")
	envStr(&c.Server.ToolsAddr, "TOOLS_ADDR")
	envStr(&c.LLM.OpenAIBaseURL, "OPENAI_BASE_URL")
	envStr(&c.LLM.OpenAIAPIKey, "OPENAI_API_KEY")
	envStr(&c.LLM.Model, "MODEL")
	envStr(&c.LLM.MiniModel, "MINI_MODEL")
	envStr(&c.LLM.VLMModel, "VLM_MODEL")
	envStr(&c.Storage.MongoURI, "MONGO_URI")
	envStr(&c.Storage.MongoDB, "MONGO_DB")
	envStr(&c.Storage.RedisAddr, "REDIS_ADDR")
	envStr(&c.Storage.BaseStoragePath, "BASE_STORAGE_PATH")
	envStr(&c.Agent.RunMode, "RUN_MODE")
	envInt(&c.Agent.CheckpointTTLSec, "CHECKPOINT_TTL_SEC")
	envStr(&c.Agent.GUIAgentWSURL, "GUI_AGENT_WS_URL")
	envStr(&c.Security.CtxTokenSecret, "CTX_TOKEN_SECRET")
	envInt(&c.Security.CtxTokenTTLSec, "CTX_TOKEN_TTL_SEC")
	envStr(&c.Obs.LogLevel, "LOG_LEVEL")
	if v := os.Getenv("CORS_ALLOWED_HOSTS"); v != "" {
		c.Server.CORSAllowedHosts = splitComma(v)
	}
}

func (c *Config) applyDefaults() {
	setDefault(&c.Server.ControlAddr, ":8080")
	setDefault(&c.Server.ToolsAddr, ":8090")
	if len(c.Server.CORSAllowedHosts) == 0 {
		c.Server.CORSAllowedHosts = []string{"localhost", "127.0.0.1"}
	}
	setDefault(&c.Storage.MongoURI, "mongodb://localhost:27017")
	setDefault(&c.Storage.MongoDB, "orka")
	setDefault(&c.Storage.RedisAddr, "localhost:6379")
	setDefault(&c.Storage.BaseStoragePath, "./data/storage")
	setDefault(&c.Agent.RunMode, "adk")
	setDefaultInt(&c.Agent.CheckpointTTLSec, 86400)
	setDefault(&c.LLM.Model, "gpt-4o-mini")
	setDefault(&c.LLM.MiniModel, c.LLM.Model)
	setDefaultInt(&c.Security.CtxTokenTTLSec, 300)
	setDefault(&c.Obs.LogLevel, "info")
	if c.Obs.PersistSampling == 0 {
		c.Obs.PersistSampling = 1.0
	}
}

func envStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func envInt(dst *int, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setDefault(dst *string, def string) {
	if *dst == "" {
		*dst = def
	}
}

func setDefaultInt(dst *int, def int) {
	if *dst == 0 {
		*dst = def
	}
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Package config loads layered configuration: config.yaml as a base, then
// environment variables override individual values. No secrets are hardcoded.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

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
	setDefault(&c.Storage.MongoDB, "cavis")
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

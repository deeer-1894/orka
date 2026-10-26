// Package config loads layered configuration: config.yaml as a base, then
// environment variables override individual values. No secrets are hardcoded.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultDevSecret is the placeholder secret shipped in config.yaml. It is
// rejected at startup outside dev mode (see Validate) so a deployment can never
// silently run with a publicly-known signing key.
const DefaultDevSecret = "dev-only-change-me"

// Deprecated limit constants remain source-compatible. Zero means no quota.
const (
	DefaultRunMaxTokens           = 0
	DefaultRunMaxWallSeconds      = 0
	DefaultRunMaxSteps            = 0
	DefaultUserDailyTokens        = 0
	DefaultUsageReservationTokens = 32_768
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
	// MiniModel is a deprecated compatibility alias of Model; it is never a
	// separate selection. A deployment has one ordered list of models, the user
	// picks one per conversation, and `auto` escalates by letting the model think
	// MORE rather than by swapping it for a bigger one (see modelRouter).
	MiniModel  string `yaml:"mini_model"`
	VLMModel   string `yaml:"vlm_model"`
	MaxRetries int    `yaml:"max_retries"` // total LLM attempts incl. first on transient 429/5xx/network (default 3)
	// MaxTokens caps a single turn's output. 0 (the default) leaves the
	// provider's own ceiling in place: too low a cap truncates a legitimately
	// large single deliverable, and a 40k-character SVG file is a real answer.
	// Set it when a model's habit of reasoning instead of acting is costing more
	// than the occasional truncated write — one run here spent 693 seconds and
	// 82,903 characters composing a file inside its reasoning without ever
	// calling a tool.
	MaxTokens int `yaml:"max_tokens"`
	// Reasoning is a raw passthrough merged into every chat request body, for
	// controlling how much a reasoning model thinks before it acts.
	//
	// A passthrough rather than a modelled option because there is no agreed
	// field. Endpoints that are otherwise OpenAI-compatible each spell it
	// differently, and one deployment commonly serves several of them at once:
	//
	//	reasoning_effort: "low"          OpenAI o-series / gpt-5, the de-facto one
	//	thinking: {type: "disabled"}     Volcengine Ark (doubao), Anthropic-compatible
	//	reasoning: {effort: "low"}       OpenRouter's unified field
	//	enable_thinking: false           Qwen / DashScope, vLLM
	//
	// Empty (the default) sends nothing and leaves today's behaviour untouched.
	// This is the ONLY lever that actually shortens thinking: max_tokens caps
	// output, and a reasoning model spends its reasoning tokens FIRST, so capping
	// output truncates the answer after the thinking has already been paid for —
	// which is exactly how a 693-second run ended mid-tag having called no tools.
	Reasoning map[string]any `yaml:"reasoning"`
	// ReasoningByModel overrides Reasoning per model name. One endpoint here
	// serves nine models across three of the conventions above, so a single
	// global setting cannot be right for all of them.
	ReasoningByModel map[string]map[string]any `yaml:"reasoning_by_model"`
	// ReasoningBudgetRunes bounds how much an agent turn may reason before it
	// produces any output or tool call; past it, the turn is stopped and the
	// reasoning handed back as the plan to act on. Enforced client-side because
	// providers do not reliably honour Reasoning. 0 = off. Skipped when the user
	// turns deep thinking on.
	ReasoningBudgetRunes int `yaml:"reasoning_budget_runes"`
	// Ordered model choices. The first is the default for Auto.
	// Legacy Model is prepended when loading older deployment configuration.
	Models []string `yaml:"models"`
}

// SelectableModels returns the legacy default followed by the ordered list,
// with whitespace/duplicates removed. MiniModel never adds an implicit choice.
func (c LLMConfig) SelectableModels() []string {
	out := make([]string, 0, len(c.Models)+2)
	seen := map[string]bool{}
	for _, m := range append([]string{c.Model}, c.Models...) {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// DefaultModel is what a request with no explicit choice runs on: the first
// entry of the list. There is no second tier to fall back to any more.
func (c LLMConfig) DefaultModel() string {
	if ms := c.SelectableModels(); len(ms) > 0 {
		return ms[0]
	}
	return ""
}

// AllowsModel reports whether a caller may run on the named model. An empty
// allow-list means only the configured default is reachable, so a request cannot
// invent a model name and have it forwarded to the provider.
func (c LLMConfig) AllowsModel(name string) bool {
	for _, m := range c.SelectableModels() {
		if m == name {
			return true
		}
	}
	return false
}

type StorageConfig struct {
	MongoURI        string `yaml:"mongo_uri"`
	MongoDB         string `yaml:"mongo_db"`
	RedisAddr       string `yaml:"redis_addr"`
	BaseStoragePath string `yaml:"base_storage_path"`
}

type AgentConfig struct {
	// Deprecated: accepted for old configuration files, ignored at runtime.
	RunMaxTokens          int `yaml:"run_max_tokens"`
	RunMaxWallSeconds     int `yaml:"run_max_wall_seconds"`
	RunMaxSteps           int `yaml:"run_max_steps"`
	RunTokenCeiling       int `yaml:"run_token_ceiling"`
	RunWallSecondsCeiling int `yaml:"run_wall_seconds_ceiling"`
	RunStepsCeiling       int `yaml:"run_steps_ceiling"`
	// Fallback reservation when a caller cannot estimate its input/output cost.
	UsageReservationTokens int `yaml:"usage_reservation_tokens"`
	// ResearchMaxCalls bounds external research per run; zero uses the built-in limit.
	ResearchMaxCalls int              `yaml:"research_max_calls"`
	CheckpointTTLSec int              `yaml:"checkpoint_ttl_sec"`
	GUIAgentWSURL    string           `yaml:"gui_agent_ws_url"`
	MultiAgent       bool             `yaml:"multi_agent"` // expose orchestrator sub-agents the model can delegate to
	SubAgents        []SubAgentConfig `yaml:"sub_agents"`  // optional custom registry; empty = built-in researcher/writer/browser/engineer
	SkillsDir        string           `yaml:"skills_dir"`  // global dir of Claude-Code-style SKILL.md packages (default ./skills)
	// Deprecated: account usage is measured without a token quota.
	UserDailyTokens int `yaml:"user_daily_tokens"`
}

// SubAgentConfig declares one orchestrator-facing sub-agent. The Name+Description
// drive the model's delegation decision (the sub-agent appears as a tool); Tools
// scopes which atomic tools it may use; MaxIters
// caps its per-delegation tool-iteration budget.
type SubAgentConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Prompt      string   `yaml:"prompt"`    // system prompt (a NEED_USER_INPUT hint is always appended)
	Tools       []string `yaml:"tools"`     // atomic tool names this agent may call
	Model       string   `yaml:"model"`     // Deprecated: ignored; delegates inherit the run's selected model
	MaxIters    int      `yaml:"max_iters"` // tool-iteration cap (default 12)
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
	if err := c.Agent.ValidateBudget(); err != nil {
		return err
	}
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
	if err := validateBudgetEnv(); err != nil {
		return nil, err
	}
	c.applyEnv()
	c.applyDefaults()
	c.Storage.BaseStoragePath = realStoragePath(c.Storage.BaseStoragePath)
	if err := c.Agent.ValidateBudget(); err != nil {
		return nil, err
	}
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
	// MODELS is a comma-separated allow-list of models the UI may offer, for
	// providers that host many behind one endpoint.
	if v, ok := os.LookupEnv("MODELS"); ok {
		c.LLM.Models = splitComma(v)
	}
	envInt(&c.LLM.MaxRetries, "LLM_MAX_RETRIES")
	envInt(&c.LLM.MaxTokens, "LLM_MAX_TOKENS")
	envJSON(&c.LLM.Reasoning, "LLM_REASONING")
	envInt(&c.LLM.ReasoningBudgetRunes, "LLM_REASONING_BUDGET_RUNES")
	envStr(&c.Storage.MongoURI, "MONGO_URI")
	envStr(&c.Storage.MongoDB, "MONGO_DB")
	envStr(&c.Storage.RedisAddr, "REDIS_ADDR")
	envStr(&c.Storage.BaseStoragePath, "BASE_STORAGE_PATH")
	envInt(&c.Agent.ResearchMaxCalls, "RESEARCH_MAX_CALLS")
	envInt(&c.Agent.CheckpointTTLSec, "CHECKPOINT_TTL_SEC")
	envStr(&c.Agent.GUIAgentWSURL, "GUI_AGENT_WS_URL")
	envStr(&c.Agent.SkillsDir, "SKILLS_DIR")
	envInt(&c.Agent.UsageReservationTokens, "USAGE_RESERVATION_TOKENS")
	if os.Getenv("MULTI_AGENT") == "1" {
		c.Agent.MultiAgent = true
	}
	envStr(&c.Security.CtxTokenSecret, "CTX_TOKEN_SECRET")
	envInt(&c.Security.CtxTokenTTLSec, "CTX_TOKEN_TTL_SEC")
	envStr(&c.Obs.LogLevel, "LOG_LEVEL")
	if v := os.Getenv("CORS_ALLOWED_HOSTS"); v != "" {
		c.Server.CORSAllowedHosts = splitComma(v)
	}
}

// realStoragePath resolves the storage root once, at startup, to its real
// absolute path. A deployment may well reach its volume through a link
// (/data -> /mnt/volume, macOS /var -> /private/var); resolving it here means
// every later path comparison sees one spelling. A path that does not exist
// yet is left for the caller to create.
func realStoragePath(p string) string {
	if p == "" {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

func (c *Config) applyDefaults() {
	c.Agent = c.Agent.WithBudgetDefaults()
	setDefault(&c.Server.ControlAddr, ":8080")
	setDefault(&c.Server.ToolsAddr, ":8090")
	if len(c.Server.CORSAllowedHosts) == 0 {
		c.Server.CORSAllowedHosts = []string{"localhost", "127.0.0.1"}
	}
	setDefault(&c.Storage.MongoURI, "mongodb://localhost:27017")
	setDefault(&c.Storage.MongoDB, "orka")
	setDefault(&c.Storage.RedisAddr, "localhost:6379")
	setDefault(&c.Storage.BaseStoragePath, "./data/storage")
	setDefault(&c.Agent.SkillsDir, "./skills")
	setDefaultInt(&c.Agent.CheckpointTTLSec, 86400)
	c.LLM.Models = c.LLM.SelectableModels()
	if len(c.LLM.Models) == 0 {
		c.LLM.Models = []string{"gpt-4o-mini"}
	}
	c.LLM.Model = c.LLM.Models[0]
	c.LLM.MiniModel = c.LLM.Model                   // old fields remain readable as aliases only
	setDefaultInt(&c.Security.CtxTokenTTLSec, 1800) // cover long agentic runs (the MCP client binds its token at run start)
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

// envJSON reads a JSON object from the environment. A malformed value is
// ignored rather than fatal: it is a tuning knob, and a typo in it must not
// stop the service from starting.
func envJSON(dst *map[string]any, key string) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return
	}
	var m map[string]any
	if json.Unmarshal([]byte(v), &m) == nil {
		*dst = m
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

// WithBudgetDefaults also supports callers constructing Config directly in tests
// or embedding Orka without the YAML loader.
func (a AgentConfig) WithBudgetDefaults() AgentConfig {
	a.RunMaxTokens, a.RunMaxWallSeconds, a.RunMaxSteps = 0, 0, 0
	a.RunTokenCeiling, a.RunWallSecondsCeiling, a.RunStepsCeiling = 0, 0, 0
	a.UserDailyTokens = 0
	setDefaultInt(&a.UsageReservationTokens, DefaultUsageReservationTokens)
	return a
}

// Only the fallback usage estimate still has configuration semantics.
func (a AgentConfig) ValidateBudget() error {
	if a.UsageReservationTokens < 0 {
		return errors.New("agent.usage_reservation_tokens must be nonnegative")
	}
	return nil
}

// Reject malformed fallback usage estimates; retired quota variables are ignored.
func validateBudgetEnv() error {
	for _, key := range []string{"USAGE_RESERVATION_TOKENS"} {
		if value, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return fmt.Errorf("%s must be a nonnegative integer", key)
			}
		}
	}
	return nil
}

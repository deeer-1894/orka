package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// settings.go — the settings a user edits from the UI, in a file they own.
//
// config.yaml is the deployment's file: it is checked in, it carries comments
// explaining every choice, and it is not something to rewrite from a web
// request. Environment variables are the operator's. Neither is a good home for
// "I want to point this at a different endpoint now", which is what actually
// changes day to day — and editing a YAML file and restarting for it is the
// friction this removes.
//
// So there is a third layer, the same shape the tools this replica imitates use
// (a JSON settings file under the user's own config directory): written by the
// settings API, read at startup, and applied OVER config.yaml and the
// environment. It wins on purpose. A panel whose saved value could be silently
// overridden by an env var left in a shell profile would read as broken, and
// the whole point of it is to be the last word.
//
// It holds the API key, so it is written 0600 and never read back out through
// the API.

// Settings is the user-editable subset of the configuration. Every field is
// optional: an empty value means "leave whatever config.yaml and the
// environment decided", which is what makes a partial save safe.
type Settings struct {
	LLM SettingsLLM `json:"llm"`
}

// SettingsLLM mirrors the parts of LLMConfig worth changing without a restart.
type SettingsLLM struct {
	BaseURL string `json:"base_url,omitempty"`
	// APIKey is a secret. It lives here because this file is the user's own and
	// mode 0600; it is never returned by the API, only replaced.
	APIKey string `json:"api_key,omitempty"`
	// Models is the ordered list, first entry the default. Nil leaves the
	// configured list alone; an explicitly empty list is not a useful state and
	// is treated the same as nil.
	Models []string `json:"models,omitempty"`
	// MaxTokens caps one turn's output; -1 means "explicitly no cap", since 0
	// cannot be distinguished from unset in JSON.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Reasoning and ReasoningByModel are the provider passthrough for reasoning
	// control. There is no agreed field for it, so this stays raw. See
	// LLMConfig.Reasoning.
	Reasoning        map[string]any            `json:"reasoning,omitempty"`
	ReasoningByModel map[string]map[string]any `json:"reasoning_by_model,omitempty"`
}

// SettingsPath returns where the user's settings file lives: $ORKA_SETTINGS, or
// ~/.orka/orka.json beside the workspace storage.
func SettingsPath() string {
	if p := strings.TrimSpace(os.Getenv("ORKA_SETTINGS")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "orka.json" // cwd: better than not working at all
	}
	return filepath.Join(home, ".orka", "orka.json")
}

// LoadSettings reads the settings file. A missing file is not an error — it is
// the normal state before anyone has saved anything.
func LoadSettings(path string) (Settings, error) {
	var s Settings
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}

// SaveSettings writes the file atomically, 0600.
//
// Atomically because a truncated settings file is worse than an old one: the
// next start would come up with no endpoint at all. Written to a temp file in
// the same directory and renamed, which is atomic on the same filesystem.
func SaveSettings(path string, s Settings) error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".orka-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Apply overlays the settings onto an LLMConfig, returning the result. Only
// fields the user actually set are applied, so saving one field does not clear
// the rest.
func (s SettingsLLM) Apply(c LLMConfig) LLMConfig {
	if v := strings.TrimSpace(s.BaseURL); v != "" {
		c.OpenAIBaseURL = v
	}
	if v := strings.TrimSpace(s.APIKey); v != "" {
		c.OpenAIAPIKey = v
	}
	if len(s.Models) > 0 {
		// The saved list replaces the configured one outright, and the legacy
		// single-model fields go with it: keeping them would silently pin an old
		// MODEL= to the front of a list the user just reordered.
		c.Models = append([]string(nil), s.Models...)
		c.Model, c.MiniModel = "", ""
	}
	switch {
	case s.MaxTokens > 0:
		c.MaxTokens = s.MaxTokens
	case s.MaxTokens < 0:
		c.MaxTokens = 0 // an explicit "no cap"
	}
	if s.Reasoning != nil {
		c.Reasoning = s.Reasoning
	}
	if s.ReasoningByModel != nil {
		c.ReasoningByModel = s.ReasoningByModel
	}
	return c
}

// Redacted returns a copy safe to hand to a client: the key is replaced by
// whether one is set. Nothing else here is secret.
func (s SettingsLLM) Redacted() (SettingsLLM, bool) {
	hasKey := strings.TrimSpace(s.APIKey) != ""
	s.APIKey = ""
	return s, hasKey
}

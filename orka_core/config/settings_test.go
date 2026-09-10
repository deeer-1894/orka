package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A partial save must not clear everything else. The panel saves one field at a
// time, so "empty means leave it alone" is the property the whole file rests on.
func TestApplyOnlyTouchesWhatWasSet(t *testing.T) {
	base := LLMConfig{
		OpenAIBaseURL: "https://configured.example/v1",
		OpenAIAPIKey:  "from-config",
		Models:        []string{"a", "b"},
		MaxTokens:     32768,
		MaxRetries:    3,
	}
	got := SettingsLLM{BaseURL: "https://saved.example/v1"}.Apply(base)
	if got.OpenAIBaseURL != "https://saved.example/v1" {
		t.Errorf("base url = %q", got.OpenAIBaseURL)
	}
	if got.OpenAIAPIKey != "from-config" {
		t.Errorf("saving a URL cleared the key: %q", got.OpenAIAPIKey)
	}
	if len(got.Models) != 2 || got.MaxTokens != 32768 || got.MaxRetries != 3 {
		t.Errorf("saving a URL disturbed the rest: %+v", got)
	}
}

// A saved list is the whole truth. Leaving the legacy single-model fields in
// place would pin an old MODEL= to the front of a list the user just reordered.
func TestASavedListReplacesTheLegacyFields(t *testing.T) {
	base := LLMConfig{Model: "legacy-main", MiniModel: "legacy-mini", Models: []string{"x"}}
	got := SettingsLLM{Models: []string{"chosen", "second"}}.Apply(base)
	if got.DefaultModel() != "chosen" {
		t.Fatalf("default = %q, want the saved list's first entry", got.DefaultModel())
	}
	for _, gone := range []string{"legacy-main", "legacy-mini", "x"} {
		if got.AllowsModel(gone) {
			t.Errorf("%q survived a saved list", gone)
		}
	}
}

// 0 cannot be told from unset in JSON, so an explicit "no cap" needs its own
// value. Without this, turning the cap off would be impossible from the UI.
func TestMaxTokensCanBeSetAndExplicitlyCleared(t *testing.T) {
	base := LLMConfig{MaxTokens: 32768}
	if got := (SettingsLLM{}).Apply(base); got.MaxTokens != 32768 {
		t.Errorf("unset changed the cap to %d", got.MaxTokens)
	}
	if got := (SettingsLLM{MaxTokens: 8192}).Apply(base); got.MaxTokens != 8192 {
		t.Errorf("cap = %d, want 8192", got.MaxTokens)
	}
	if got := (SettingsLLM{MaxTokens: -1}).Apply(base); got.MaxTokens != 0 {
		t.Errorf("explicit no-cap gave %d", got.MaxTokens)
	}
}

// The key is never handed back to a client.
func TestRedactedDropsTheKeyButReportsIt(t *testing.T) {
	got, has := SettingsLLM{APIKey: "secret-value", BaseURL: "https://x/v1"}.Redacted()
	if got.APIKey != "" {
		t.Fatalf("the key survived redaction: %q", got.APIKey)
	}
	if !has {
		t.Error("a set key was reported as absent")
	}
	if got.BaseURL != "https://x/v1" {
		t.Error("redaction dropped a non-secret field")
	}
	if _, has := (SettingsLLM{}).Redacted(); has {
		t.Error("an unset key was reported as present")
	}
}

func TestRoundTripThroughTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "orka.json")
	want := Settings{LLM: SettingsLLM{
		BaseURL: "https://x/v1", APIKey: "k", Models: []string{"m1", "m2"},
		Reasoning: map[string]any{"thinking": map[string]any{"type": "disabled"}},
	}}
	if err := SaveSettings(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.LLM.BaseURL != want.LLM.BaseURL || got.LLM.APIKey != want.LLM.APIKey ||
		len(got.LLM.Models) != 2 || got.LLM.Reasoning == nil {
		t.Fatalf("round trip lost data: %+v", got.LLM)
	}
	// It holds an API key: it must not be world-readable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("settings file mode is %o, want 600 — it holds the API key", perm)
	}
}

// A missing file is the normal state before anyone saves, not an error.
func TestAMissingFileIsNotAnError(t *testing.T) {
	got, err := LoadSettings(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.LLM.BaseURL != "" {
		t.Errorf("got %+v", got)
	}
}

func TestSettingsPathHonoursTheOverride(t *testing.T) {
	t.Setenv("ORKA_SETTINGS", "/tmp/custom-orka.json")
	if got := SettingsPath(); got != "/tmp/custom-orka.json" {
		t.Fatalf("path = %q", got)
	}
	t.Setenv("ORKA_SETTINGS", "")
	if got := SettingsPath(); filepath.Base(got) != "orka.json" {
		t.Fatalf("default path = %q, want …/orka.json", got)
	}
}

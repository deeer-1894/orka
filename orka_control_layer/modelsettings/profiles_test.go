package modelsettings

import (
	"encoding/json"
	"github.com/orka-oss/orka_core/modelprofile"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesMigrateLegacyAndKeepKeysScoped(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	_, err := s.Save("alice", Config{BaseURL: "https://one.test/v1", Models: []string{"first"}, Enabled: true}, "legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProfiles("alice")
	if err != nil || len(p.Profiles) != 1 || p.ActiveProfileID != "legacy" || p.Profiles[0].APIKey != "legacy-secret" {
		t.Fatal("migration failed", err)
	}
	p.Profiles = append(p.Profiles, Config{ID: "work", Name: "Work", Protocol: "openai-compatible", BaseURL: "https://one.test/v1", Models: []string{"manual"}, Enabled: true})
	p.ActiveProfileID = "work"
	saved, err := s.SaveProfiles("alice", p, map[string]string{"work": "work-secret"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(saved.Public())
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), `"api_key":`) {
		t.Fatal("leaked key")
	}
	active, _, err := s.Get("alice")
	if err != nil || active.ID != "work" || active.APIKey != "work-secret" {
		t.Fatal("wrong active profile", err)
	}
	// Same URL but different IDs must not inherit one another's credentials.
	p = saved
	p.Profiles = append(p.Profiles, Config{ID: "new", Name: "New", BaseURL: "https://one.test/v1", Models: []string{"x"}})
	p.Profiles[1].BaseURL = "https://two.test/v1"
	saved, err = s.SaveProfiles("alice", p, nil)
	if err != nil || saved.Profiles[1].APIKey != "" || saved.Profiles[2].APIKey != "" {
		t.Fatal("key crossed connection identity", err)
	}
	reread, err := New(filepath.Join(filepath.Dir(s.dir), "storage")).GetProfiles("alice")
	if err != nil || len(reread.Profiles) != 3 {
		t.Fatal("restart failed", err)
	}
	other, err := s.GetProfiles("bob")
	if err != nil || len(other.Profiles) != 0 {
		t.Fatal("owner leak", err)
	}
	// Legacy edits update the active profile without destroying named profiles.
	_, err = s.Save("alice", Config{Enabled: false}, "")
	if err != nil {
		t.Fatal(err)
	}
	reread, err = s.GetProfiles("alice")
	if err != nil || len(reread.Profiles) != 3 || reread.Profiles[1].Enabled {
		t.Fatal("legacy edit destroyed profiles", err)
	}
}

func TestProfilesRejectAmbiguousAndUnsupportedConfiguration(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	p := Config{ID: "a", Name: "A", BaseURL: "http://local/v1", Models: []string{"m"}, Enabled: true}
	for _, v := range []Profiles{
		{Profiles: []Config{p, p}, ActiveProfileID: "a"},
		{Profiles: []Config{p}, ActiveProfileID: "missing"},
	} {
		if _, err := s.SaveProfiles("alice", v, nil); err == nil {
			t.Fatal("invalid profiles accepted")
		}
	}
	p.Protocol = "invented"
	if _, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{p}, ActiveProfileID: "a"}, nil); err == nil {
		t.Fatal("unknown protocol accepted")
	}
}

func TestClearingProfilesStaysEmptyAndLegacyCanRecreate(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	_, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProfiles("alice")
	if err != nil || len(p.Profiles) != 0 {
		t.Fatal("empty collection became legacy", p, err)
	}
	_, err = s.Save("alice", Config{BaseURL: "http://localhost/v1", Models: []string{"m"}, Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.GetProfiles("alice")
	if err != nil || len(p.Profiles) != 1 || p.ActiveProfileID != "legacy" {
		t.Fatal("legacy recreate failed", p, err)
	}
}

func TestLegacyMigrationPreservesMeasuredPoliciesWithoutVerifiedCapabilities(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	if _, err := s.Save("alice", Config{BaseURL: "http://localhost/v1", Models: []string{"glm-5.3-flash", "glm-5.3", "unknown"}, Enabled: true}, "test-key"); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProfiles("alice")
	if err != nil {
		t.Fatal(err)
	}
	c := p.Profiles[0]
	flash, glm := c.Policies["glm-5.3-flash"], c.Policies["glm-5.3"]
	if flash.FirstMaxTokens != 8192 || flash.MaxTokens != 8192 || flash.TimeoutSeconds != 300 || flash.ReasoningEffort != "low" || glm.FirstMaxTokens != 16384 || glm.MaxTokens != 16384 || glm.TimeoutSeconds != 300 || glm.ReasoningEffort != "low" {
		t.Fatal("migration lost measured policy", c.Policies)
	}
	if _, ok := c.Policies["unknown"]; ok {
		t.Fatal("guessed unknown policy")
	}
	if len(c.Verified) != 0 {
		t.Fatal("migration falsely verified capabilities")
	}
	saved, err := s.SaveProfiles("alice", p, nil)
	if err != nil || saved.Profiles[0].Policies["glm-5.3-flash"] != flash {
		t.Fatal("migration wasn't persistent", err)
	}
	// An explicit edit of migrated policy must remain editable on subsequent reads.
	saved.Profiles[0].Policies["glm-5.3-flash"] = modelprofile.CallPolicy{MaxTokens: 9000, TimeoutSeconds: 400}
	s.SaveProfiles("alice", saved, nil)
	again, err := s.GetProfiles("alice")
	if err != nil || again.Profiles[0].Policies["glm-5.3-flash"].MaxTokens != 9000 {
		t.Fatal("edited migration overwritten")
	}
}
func TestSaveRejectsNativeProtocolsUntilImplemented(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	for _, protocol := range []string{"anthropic", "gemini"} {
		c := Config{ID: "native", Name: "Native", Protocol: protocol, BaseURL: "http://localhost/v1", Models: []string{"m"}, Enabled: true}
		if _, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{c}, ActiveProfileID: "native"}, nil); err == nil {
			t.Fatal("unsupported executable protocol saved", protocol)
		}
	}
}

func TestLegacyEditingMigratedProfileDoesNotDropPolicies(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	s.Save("alice", Config{BaseURL: "http://localhost/v1", Models: []string{"glm-5.3-flash"}, Enabled: true}, "")
	p, _ := s.GetProfiles("alice")
	s.SaveProfiles("alice", p, nil)
	if _, err := s.Save("alice", Config{BaseURL: "http://localhost/v1", Models: []string{"glm-5.3-flash"}, Enabled: true}, ""); err != nil {
		t.Fatal(err)
	}
	c, _, err := s.Get("alice")
	if err != nil || c.Policies["glm-5.3-flash"].TimeoutSeconds != 300 {
		t.Fatal("old settings payload dropped migrated tuning", c.Policies, err)
	}
	p, _ = s.GetProfiles("alice")
	p.Profiles[0].Policies = nil // old clients do not send the optional field
	saved, err := s.SaveProfiles("alice", p, nil)
	if err != nil || saved.Profiles[0].Policies["glm-5.3-flash"].TimeoutSeconds != 300 {
		t.Fatal("omitted policies reset tuning", err)
	}
	p.Profiles[0].Policies = map[string]modelprofile.CallPolicy{} // explicit clear remains supported
	saved, err = s.SaveProfiles("alice", p, nil)
	if err != nil || len(saved.Profiles[0].Policies) != 0 {
		t.Fatal("explicit clear ignored", err)
	}
}

func TestUnattemptedCapabilityDoesNotExposeFakeTimestamp(t *testing.T) {
	b, err := json.Marshal(Check{})
	if err != nil || strings.Contains(string(b), "checked_at") {
		t.Fatal("unattempted capability acquired a verification date", string(b), err)
	}
}

func TestProfilesRejectReservedDeploymentIDWithoutReplacingActive(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "storage"))
	c := Config{ID: "user", Name: "User", BaseURL: "http://local/v1", Models: []string{"glm-5.3-flash"}, Enabled: true}
	if _, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{c}, ActiveProfileID: c.ID}, map[string]string{c.ID: "private-key"}); err != nil {
		t.Fatal(err)
	}
	c.ID = "deployment"
	if _, err := s.SaveProfiles("alice", Profiles{Profiles: []Config{c}, ActiveProfileID: c.ID}, nil); err == nil {
		t.Fatal("user profile accepted reserved deployment identity and can bypass explicit policies")
	}
	got, err := s.GetProfiles("alice")
	if err != nil || got.ActiveProfileID != "user" || len(got.Profiles) != 1 || got.Profiles[0].APIKey != "private-key" {
		t.Fatal("rejected save altered active profile or its private credential")
	}
}

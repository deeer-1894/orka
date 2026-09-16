package config

import (
	"testing"
)

func TestValidateSecret(t *testing.T) {
	t.Setenv("ORKA_DEV", "")

	c := &Config{}
	c.Security.CtxTokenSecret = DefaultDevSecret
	if err := c.Validate(); err == nil {
		t.Error("default placeholder secret should be rejected")
	}
	c.Security.CtxTokenSecret = ""
	if err := c.Validate(); err == nil {
		t.Error("empty secret should be rejected")
	}
	c.Security.CtxTokenSecret = "short"
	if err := c.Validate(); err == nil {
		t.Error("short secret should be rejected")
	}
	c.Security.CtxTokenSecret = "a-properly-long-random-secret-value"
	if err := c.Validate(); err != nil {
		t.Errorf("strong secret should pass: %v", err)
	}

	// dev mode relaxes the check
	t.Setenv("ORKA_DEV", "1")
	c.Security.CtxTokenSecret = DefaultDevSecret
	if err := c.Validate(); err != nil {
		t.Errorf("dev mode should allow placeholder: %v", err)
	}
}

func TestSessionSecretDistinct(t *testing.T) {
	c := &Config{}
	c.Security.CtxTokenSecret = "a-properly-long-random-secret-value"
	if c.SessionSecret() == string(c.CtxSecret()) {
		t.Error("session secret must differ from ctx secret")
	}
}

func TestLoadDefaultsWhenNoFile(t *testing.T) {
	c, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.ControlAddr != ":8080" {
		t.Fatalf("default control addr = %s", c.Server.ControlAddr)
	}
	if c.Agent.CheckpointTTLSec != 86400 {
		t.Fatalf("default ttl = %d", c.Agent.CheckpointTTLSec)
	}
	if c.Obs.PersistSampling != 1.0 {
		t.Fatalf("default sampling = %v", c.Obs.PersistSampling)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("CHECKPOINT_TTL_SEC", "120")
	t.Setenv("CORS_ALLOWED_HOSTS", "a.com, b.com ,c.com")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.CheckpointTTLSec != 120 {
		t.Fatalf("ttl = %d", c.Agent.CheckpointTTLSec)
	}
	if len(c.Server.CORSAllowedHosts) != 3 || c.Server.CORSAllowedHosts[1] != "b.com" {
		t.Fatalf("cors hosts = %v", c.Server.CORSAllowedHosts)
	}
}

func TestModelListHasNoImplicitMiniTier(t *testing.T) {
	c := LLMConfig{Model: " first ", MiniModel: "hidden", Models: []string{"other", "first", "mini"}}
	got := c.SelectableModels()
	if len(got) != 3 || got[0] != "first" || got[1] != "other" || got[2] != "mini" || c.AllowsModel("hidden") {
		t.Fatal(got)
	}
}

func TestModelDefaultsUseOrderedList(t *testing.T) {
	for _, tc := range []struct {
		legacy string
		models []string
		want   string
	}{
		{models: []string{"listed", "other"}, want: "listed"},
		{legacy: "old", models: []string{"listed", "old"}, want: "old"},
		{want: "gpt-4o-mini"},
	} {
		c := Config{LLM: LLMConfig{Model: tc.legacy, MiniModel: "ignored", Models: tc.models}}
		c.applyDefaults()
		if c.LLM.Model != tc.want || c.LLM.MiniModel != tc.want || c.LLM.Models[0] != tc.want || c.LLM.AllowsModel("ignored") {
			t.Fatalf("default=%s alias=%s list=%v", c.LLM.Model, c.LLM.MiniModel, c.LLM.Models)
		}
	}
}

func TestLegacyLimitsAreIgnoredAfterConfigLoad(t *testing.T) {
	for _, value := range []string{"1", "-1", "oops", "99999999999999999999999"} {
		t.Setenv("ORKA_DEV", "1")
		t.Setenv("RUN_MAX_TOKENS", value)
		t.Setenv("RUN_MAX_WALL_SECONDS", value)
		t.Setenv("RUN_MAX_STEPS", value)
		t.Setenv("USER_DAILY_TOKENS", value)
		c, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		a := c.Agent
		if a.RunMaxTokens != 0 || a.RunMaxSteps != 0 || a.RunMaxWallSeconds != 0 || a.UserDailyTokens != 0 {
			t.Fatalf("legacy quota %+v", a)
		}
	}
}
func TestUsageEstimateValidation(t *testing.T) {
	for _, value := range []string{"oops", "-1", "99999999999999999999999"} {
		t.Setenv("USAGE_RESERVATION_TOKENS", value)
		if _, err := Load(""); err == nil {
			t.Fatalf("accepted invalid estimate %s", value)
		}
	}
}

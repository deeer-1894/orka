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

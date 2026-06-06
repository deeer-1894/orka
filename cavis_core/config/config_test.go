package config

import (
	"testing"
)

func TestLoadDefaultsWhenNoFile(t *testing.T) {
	c, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.ControlAddr != ":8080" {
		t.Fatalf("default control addr = %s", c.Server.ControlAddr)
	}
	if c.Agent.RunMode != "adk" {
		t.Fatalf("default run mode = %s", c.Agent.RunMode)
	}
	if c.Agent.CheckpointTTLSec != 86400 {
		t.Fatalf("default ttl = %d", c.Agent.CheckpointTTLSec)
	}
	if c.Obs.PersistSampling != 1.0 {
		t.Fatalf("default sampling = %v", c.Obs.PersistSampling)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("RUN_MODE", "graph")
	t.Setenv("CHECKPOINT_TTL_SEC", "120")
	t.Setenv("CORS_ALLOWED_HOSTS", "a.com, b.com ,c.com")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.RunMode != "graph" {
		t.Fatalf("run mode = %s", c.Agent.RunMode)
	}
	if c.Agent.CheckpointTTLSec != 120 {
		t.Fatalf("ttl = %d", c.Agent.CheckpointTTLSec)
	}
	if len(c.Server.CORSAllowedHosts) != 3 || c.Server.CORSAllowedHosts[1] != "b.com" {
		t.Fatalf("cors hosts = %v", c.Server.CORSAllowedHosts)
	}
}

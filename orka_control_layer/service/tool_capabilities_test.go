package service

import (
	"context"
	"github.com/orka-oss/orka_core/agent"
	"testing"
)

func TestQuantToolsDefaultAndExplicitRange(t *testing.T) {
	prior := QuantTools
	QuantTools = []agent.BaseTool{validateFactorTool{}}
	defer func() { QuantTools = prior }()
	for _, tc := range []struct {
		ctx     context.Context
		enabled []string
		want    bool
	}{{context.Background(), nil, true}, {context.Background(), []string{"web"}, false}, {WithQuantCapability(context.Background()), nil, true}, {context.Background(), []string{"quant"}, true}} {
		found := false
		for _, tool := range localCapabilityTools(tc.ctx, ChatRunRequest{EnabledTools: tc.enabled}) {
			if tool.Name() == "validate_factor" {
				found = true
			}
		}
		if found != tc.want {
			t.Fatalf("quant visible=%v want %v", found, tc.want)
		}
	}
}
func TestUnavailableGUINeverClaimsCompletion(t *testing.T) {
	out, err := (guiUnavailableTool{}).Invoke(context.Background(), map[string]any{"instruction": "do work"})
	if err == nil || out != "" {
		t.Fatal("fake completion")
	}
}

func TestCodeScopeAvailableByDefaultAndCanBeRestricted(t *testing.T) {
	for _, tc := range []struct {
		tools []string
		want  bool
	}{{nil, true}, {[]string{}, true}, {[]string{"file", "web"}, false}, {[]string{"code"}, true}, {[]string{"python"}, true}} {
		if got := requestedExecutionScope(withRequestedExecutionScope(context.Background(), ChatRunRequest{EnabledTools: tc.tools})); got != tc.want {
			t.Fatalf("scope %v: %v", tc.tools, got)
		}
	}
}

func TestCatalogNeverReceivesCodeExecutionScope(t *testing.T) {
	ctx := context.WithValue(context.Background(), catalogContextKey{}, true)
	for _, selection := range [][]string{nil, {"code"}} {
		if requestedExecutionScope(withRequestedExecutionScope(ctx, ChatRunRequest{EnabledTools: selection})) {
			t.Fatal("metadata discovery received execution authority")
		}
	}
}

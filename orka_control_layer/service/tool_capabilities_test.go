package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/modelprofile"
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

func baseToolNames(tools []agent.BaseTool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		out[tool.Name()] = true
	}
	return out
}

// Wording once selected the catalog: research or "访问" requests lost file_write
// and code execution, and a request naming update_plan did not get it.
func TestRequestWordingDoesNotNarrowTools(t *testing.T) {
	ctx := modelprofile.WithContext(context.Background(), modelprofile.Snapshot{Capabilities: modelprofile.Capabilities{Tools: true}})
	all := []agent.BaseTool{namedTool{"file_write"}, namedTool{"shell"}, namedTool{"web_search"}, namedTool{"browser"}}
	for _, msg := range []string{
		"调研三家云厂商最新的 GPU 价格并对比,写成 report.md 保存到工作区",
		"访问数据库里的订单表,统计本月销量",
		"只能通过浏览器打开官网",
	} {
		got := baseToolNames(filterToolsForRequest(ctx, all, ChatRunRequest{Message: msg}))
		for _, tool := range all {
			if !got[tool.Name()] {
				t.Errorf("%q removed %s", msg, tool.Name())
			}
		}
	}
	got := baseToolNames(filterToolsForRequest(ctx, all, ChatRunRequest{EnabledTools: []string{"web_search"}}))
	if len(got) != 1 || !got["web_search"] {
		t.Fatalf("declared whitelist not applied: %v", got)
	}
	controls := baseToolNames(mainAgentTools(nil))
	for _, name := range []string{planToolName, "clarify", findToolsName} {
		if !controls[name] {
			t.Errorf("plan owner lacks %s", name)
		}
	}
}

func TestRunAgentRequiresVerifiedVision(t *testing.T) {
	tools := []agent.BaseTool{namedTool{"browser"}, namedTool{"run_agent"}}
	for _, tc := range []struct {
		ctx  context.Context
		want bool
	}{
		{context.Background(), false},
		{modelprofile.WithContext(context.Background(), modelprofile.Snapshot{Capabilities: modelprofile.Capabilities{Tools: true}}), false},
		{modelprofile.WithContext(context.Background(), modelprofile.Snapshot{Capabilities: modelprofile.Capabilities{Tools: true, Vision: true}}), true},
	} {
		got := baseToolNames(filterToolsForRequest(tc.ctx, tools, ChatRunRequest{}))
		if !got["browser"] || got["run_agent"] != tc.want {
			t.Errorf("tools=%v want run_agent=%v", got, tc.want)
		}
	}
}

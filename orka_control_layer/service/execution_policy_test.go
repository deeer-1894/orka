package service

import (
	"context"
	"testing"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/modelprofile"
)

type policyTool struct{ name string }

func (t policyTool) Name() string                                           { return t.name }
func (t policyTool) Description() string                                    { return t.name }
func (t policyTool) Schema() map[string]any                                 { return map[string]any{"type": "object"} }
func (t policyTool) Invoke(context.Context, map[string]any) (string, error) { return "ok", nil }

func policyToolNames(tools []agent.BaseTool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		out[tool.Name()] = true
	}
	return out
}

func TestExecutionPolicyEnforcesBrowserOnlyAtTheToolBoundary(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "只能通过浏览器访问网页完成，打开三个网站交叉验证"})
	if policy.Mode != executionBrowser || !policy.StrictSources {
		t.Fatalf("browser-only request compiled as %+v", policy)
	}
	tools := []agent.BaseTool{
		policyTool{"browser"}, policyTool{"run_agent"}, policyTool{"web_search"},
		policyTool{"fetch_url"}, policyTool{"http_request"}, policyTool{"python"},
		policyTool{"shell"}, policyTool{"file_write"}, policyTool{"calculator"},
	}
	got := policyToolNames(filterToolsByPolicy(tools, policy))
	for _, name := range []string{"browser", "run_agent"} {
		if !got[name] {
			t.Errorf("allowed tool %q was removed", name)
		}
	}
	for _, name := range []string{"web_search", "fetch_url", "http_request", "python", "shell", "file_write", "calculator"} {
		if got[name] {
			t.Errorf("browser-only policy leaked %q", name)
		}
	}
}

func TestExecutionPolicyDoesNotTreatProhibitedCodeAsRequestedCode(t *testing.T) {
	prompt := "只能通过浏览器访问网页完成，不得使用网页搜索、直连请求、Python 执行或终端，也不要创建脚本、测试或文件"
	policy := compileExecutionPolicy(ChatRunRequest{Message: prompt})
	if !policy.StrictSources || !policy.SourceVerificationOnly || policy.Mode != executionBrowser {
		t.Fatalf("prohibited code was treated as requested code: %+v", policy)
	}

	positive := compileExecutionPolicy(ChatRunRequest{Message: "只能通过浏览器获取网页，但用 Python 分析下载的 CSV"})
	if positive.SourceVerificationOnly || positive.Mode != executionBrowser {
		t.Fatalf("affirmative code after contrast was ignored: %+v", positive)
	}
}

func TestSourceVerificationUsesOnlyRelevantAgentControls(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "只能通过浏览器访问网页完成，打开三个网站交叉验证"})
	ctx := withExecutionPolicy(context.Background(), policy)
	got := policyToolNames(mainAgentTools(ctx, []agent.BaseTool{policyTool{"browser"}, policyTool{"run_agent"}}))

	for _, name := range []string{"browser", "run_agent", planToolName, "clarify"} {
		if !got[name] {
			t.Errorf("required tool %q was removed", name)
		}
	}
	for _, name := range []string{findToolsName, "check_delivery", "check_acceptance"} {
		if got[name] {
			t.Errorf("source-verification task exposed irrelevant tool %q", name)
		}
	}
}

func TestSimpleBrowserSummaryUsesMinimalToolSurface(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "打开 Hacker News，读取首页前 10 条并整理中文摘要"})
	if policy.Mode != executionBrowser || !policy.SourceVerificationOnly || policy.NeedsPlan {
		t.Fatalf("simple browser summary policy = %+v", policy)
	}
	ctx := modelprofile.WithContext(context.Background(), modelprofile.Snapshot{
		Capabilities: modelprofile.Capabilities{Tools: true},
	})
	req := ChatRunRequest{executionPolicy: &policy}
	got := policyToolNames(filterToolsForRequest(ctx, []agent.BaseTool{
		policyTool{"browser"}, policyTool{"run_agent"}, policyTool{"fetch_url"},
		policyTool{"search_evidence"}, policyTool{"file_read"}, policyTool{"file_write"},
		policyTool{"python"}, policyTool{"shell"}, policyTool{"calculator"},
	}, req))
	for _, name := range []string{"browser", "fetch_url", "search_evidence", "file_read"} {
		if !got[name] {
			t.Errorf("source tool %q was removed", name)
		}
	}
	for _, name := range []string{"run_agent", "file_write", "python", "shell", "calculator"} {
		if got[name] {
			t.Errorf("irrelevant or unavailable tool %q remained visible", name)
		}
	}
	controls := policyToolNames(mainAgentTools(withExecutionPolicy(ctx, policy), []agent.BaseTool{policyTool{"browser"}}))
	if controls[planToolName] || controls[findToolsName] || !controls["clarify"] {
		t.Fatalf("simple source controls = %v", controls)
	}
}

func TestVerifiedVisionKeepsGUIAgentAvailable(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "浏览器打开网页并总结"})
	ctx := modelprofile.WithContext(context.Background(), modelprofile.Snapshot{
		Capabilities: modelprofile.Capabilities{Tools: true, Vision: true},
	})
	got := policyToolNames(filterToolsForRequest(ctx, []agent.BaseTool{policyTool{"browser"}, policyTool{"run_agent"}}, ChatRunRequest{executionPolicy: &policy}))
	if !got["browser"] || !got["run_agent"] {
		t.Fatalf("verified GUI tools = %v", got)
	}
}

func TestExecutionPolicyAvoidsCodeForQualitativeResearch(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "调研今天的行业新闻并给出带来源的总结"})
	if policy.Mode != executionResearch || !policy.SourceVerificationOnly {
		t.Fatalf("qualitative research compiled as %+v", policy)
	}
	got := policyToolNames(filterToolsByPolicy([]agent.BaseTool{
		policyTool{"web_search"}, policyTool{"fetch_url"}, policyTool{"python"}, policyTool{"shell"},
	}, policy))
	if !got["web_search"] || !got["fetch_url"] || got["python"] || got["shell"] {
		t.Fatalf("qualitative research tools = %v", got)
	}
}

func TestExecutionPolicyKeepsCodeForEngineeringAndDataWork(t *testing.T) {
	for _, prompt := range []string{
		"实现一个 Go 编程项目并运行测试",
		"分析 CSV 数据并生成 Excel 文件",
	} {
		policy := compileExecutionPolicy(ChatRunRequest{Message: prompt})
		got := policyToolNames(filterToolsByPolicy([]agent.BaseTool{policyTool{"python"}, policyTool{"shell"}}, policy))
		if !got["python"] || !got["shell"] {
			t.Fatalf("%q unexpectedly removed execution tools: %+v %v", prompt, policy, got)
		}
	}
}

func TestExecutionPolicyUsesDeepAgentOnlyForIndependentFanout(t *testing.T) {
	parallel := compileExecutionPolicy(ChatRunRequest{Message: "调研并比较三个不同框架，每个至少查两个独立来源"})
	if !parallel.UseDeepAgent || parallel.MaxWorkers != 3 {
		t.Fatalf("independent fanout policy = %+v", parallel)
	}
	strictBrowser := compileExecutionPolicy(ChatRunRequest{Message: "只能通过浏览器比较三个网站"})
	if strictBrowser.UseDeepAgent {
		t.Fatal("shared browser page was parallelized before isolated lanes exist")
	}
	simple := compileExecutionPolicy(ChatRunRequest{Message: "解释什么是幂等性"})
	if simple.UseDeepAgent || simple.Mode != executionDirect {
		t.Fatalf("simple question policy = %+v", simple)
	}
}

func TestStrictBrowserPolicyOverridesGlobalMultiAgent(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "只能通过浏览器比较三个网站"})
	ctx := withExecutionPolicy(context.Background(), policy)
	service := &ChatService{Cfg: &config.Config{Agent: config.AgentConfig{MultiAgent: true}}}
	if service.deepAgentEnabled(ctx) {
		t.Fatal("global multi-agent setting bypassed the single-browser-page invariant")
	}
}

func TestExecutionPolicyInstructionsStateEnforcedConstraints(t *testing.T) {
	policy := compileExecutionPolicy(ChatRunRequest{Message: "浏览器打开彭博社官网，给我今日新闻总结"})
	text := policy.decorateInstruction("base")
	for _, phrase := range []string{"base", "browser", "Do not create scripts", "server-enforced"} {
		if !contains(text, phrase) {
			t.Errorf("instruction missing %q: %s", phrase, text)
		}
	}
}

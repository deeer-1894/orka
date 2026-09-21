package service

import (
	"context"
	"strings"
	"unicode"

	"github.com/orka-oss/orka_core/agent"
)

type executionMode string

const (
	executionDirect   executionMode = "direct"
	executionStandard executionMode = "standard"
	executionResearch executionMode = "research"
	executionBrowser  executionMode = "browser"
	executionCoding   executionMode = "coding"
)

// executionPolicy is the server-side contract for one run. It converts request
// semantics into enforceable capabilities before any tool catalogue is built.
// Prompts explain the contract, but only AllowsTool enforces it.
type executionPolicy struct {
	Mode                   executionMode
	StrictSources          bool
	SourceVerificationOnly bool
	UseDeepAgent           bool
	MaxWorkers             int
}

type executionPolicyKey struct{}

func withExecutionPolicy(ctx context.Context, policy executionPolicy) context.Context {
	return context.WithValue(ctx, executionPolicyKey{}, policy)
}

func executionPolicyFrom(ctx context.Context) executionPolicy {
	if policy, ok := ctx.Value(executionPolicyKey{}).(executionPolicy); ok {
		return policy
	}
	return executionPolicy{Mode: executionStandard, MaxWorkers: 3}
}

func compileExecutionPolicy(req ChatRunRequest) executionPolicy {
	text := normalizePolicyText(req.Message)
	policy := executionPolicy{Mode: executionDirect, MaxWorkers: 3}

	browser := containsPolicyTerm(text,
		"浏览器", "browser", "网页操作", "点击网页", "填写网页", "打开官网", "打开网站")
	strictBrowser := browser && containsPolicyTerm(text,
		"只能通过浏览器", "仅通过浏览器", "只用浏览器", "只允许浏览器", "必须通过浏览器",
		"browser only", "only through the browser", "浏览器打开")
	coding := containsPolicyTerm(text,
		"编程", "写代码", "实现代码", "代码项目", "修复代码", "运行测试", "单元测试", "集成测试",
		"python", "golang", " go ", "typescript", "javascript", "build", "compile", "coding")
	dataWork := containsPolicyTerm(text,
		"csv", "excel", "xlsx", "数据处理", "数据分析", "生成表格", "电子表格")
	research := containsPolicyTerm(text,
		"调研", "研究", "查询", "检索", "搜索", "最新", "新闻", "来源", "交叉验证", "比较", "对比",
		"research", "latest", "sources", "compare")

	switch {
	case strictBrowser:
		policy.Mode = executionBrowser
		policy.StrictSources = true
		policy.SourceVerificationOnly = !coding && !dataWork
	case coding || dataWork:
		policy.Mode = executionCoding
	case browser:
		policy.Mode = executionBrowser
		policy.SourceVerificationOnly = research && !dataWork
	case research:
		policy.Mode = executionResearch
		policy.SourceVerificationOnly = !dataWork
	default:
		policy.Mode = executionDirect
	}

	// DeepAgent is valuable when isolated branches can converge independently.
	// One shared browser page is deliberately excluded until browser lanes exist.
	complex := containsPolicyTerm(text,
		"至少", "多个", "不同来源", "独立来源", "跨网站", "三个", "3个", "四个", "4个",
		"完整项目", "复杂任务", "多阶段", "分别", "multiple", "at least", "cross-site")
	policy.UseDeepAgent = !policy.StrictSources && complex && (policy.Mode == executionResearch || policy.Mode == executionCoding)
	return policy
}

func normalizePolicyText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte(' ')
	space := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	b.WriteByte(' ')
	return b.String()
}

func containsPolicyTerm(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func (p executionPolicy) allowsTool(tool agent.BaseTool) bool {
	if tool == nil {
		return false
	}
	name, group := tool.Name(), toolGroup(tool)
	if p.StrictSources {
		switch group {
		case "browser", "gui_agent", "file", "artifact", "util", "office", "skill":
			return true
		}
		return name == "browser" || name == "run_agent"
	}
	if p.SourceVerificationOnly && (name == "python" || name == "shell" || group == "code" || group == "shell") {
		return false
	}
	return true
}

func (p executionPolicy) allowsExecution() bool {
	return !p.StrictSources && !p.SourceVerificationOnly
}

func filterToolsByPolicy(tools []agent.BaseTool, policy executionPolicy) []agent.BaseTool {
	out := make([]agent.BaseTool, 0, len(tools))
	for _, tool := range tools {
		if policy.allowsTool(tool) {
			out = append(out, tool)
		}
	}
	return out
}

func filterToolsForRequest(tools []agent.BaseTool, req ChatRunRequest) []agent.BaseTool {
	tools = filterEnabled(tools, req.EnabledTools)
	if req.executionPolicy != nil {
		tools = filterToolsByPolicy(tools, *req.executionPolicy)
	}
	return tools
}

func (s *ChatService) deepAgentEnabled(ctx context.Context) bool {
	policy := executionPolicyFrom(ctx)
	if policy.StrictSources {
		return false
	}
	configured := s != nil && s.Cfg != nil && s.Cfg.Agent.MultiAgent
	return configured || policy.UseDeepAgent
}

func (p executionPolicy) decorateInstruction(base string) string {
	var rules []string
	if p.StrictSources {
		rules = append(rules, "This run has a server-enforced browser-only source policy. Obtain online information with browser or run_agent. Web search, direct HTTP and code execution are unavailable and must not be simulated.")
	}
	if p.SourceVerificationOnly {
		rules = append(rules, "This is a source-verification task. Verify source, date and coverage, then answer directly. Do not create scripts, tests, build steps or acceptance files unless the user explicitly asks for a software or data artifact.")
	}
	if p.UseDeepAgent {
		rules = append(rules, "Use at most three independent workers. Launch independent branches together, then synthesize their receipts without repeating their research.")
	}
	if len(rules) == 0 {
		return base
	}
	return strings.TrimSpace(base) + "\n\n[Execution policy]\n" + strings.Join(rules, "\n")
}

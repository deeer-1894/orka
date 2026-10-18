package service

import (
	"context"
	"strings"
	"unicode"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/modelprofile"
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
	NeedsPlan              bool
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
	if !browser && containsPolicyTerm(text, "访问", "打开 ") &&
		!containsPolicyTerm(text, "文件", "工作区", "目录") {
		browser = true
	}
	strictBrowser := browser && containsPolicyTerm(text,
		"只能通过浏览器", "仅通过浏览器", "只用浏览器", "只允许浏览器", "必须通过浏览器",
		"browser only", "only through the browser", "浏览器打开")
	coding := containsAffirmativePolicyTerm(text,
		"编程", "写代码", "实现代码", "代码项目", "修复代码", "运行测试", "单元测试", "集成测试",
		"python", "golang", " go ", "typescript", "javascript", "build", "compile", "coding")
	dataWork := containsPolicyTerm(text,
		"csv", "excel", "xlsx", "数据处理", "数据分析", "生成表格", "电子表格")
	deliverableWork := containsPolicyTerm(text,
		"报告", "文档", "方案", "交付", "write report", "create report", "deliver a report")
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
		policy.SourceVerificationOnly = !coding && !dataWork
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
	policy.NeedsPlan = coding || dataWork || deliverableWork || complex
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

// containsAffirmativePolicyTerm ignores capability words that occur inside a
// local prohibition. This matters for constrained prompts such as "do not use
// Python": a keyword-only router otherwise expands the run into a coding task.
// Contrast markers start a new local scope so "do not browse, but use Python"
// still records the affirmative requirement.
func containsAffirmativePolicyTerm(text string, terms ...string) bool {
	for _, term := range terms {
		from := 0
		for from < len(text) {
			rel := strings.Index(text[from:], term)
			if rel < 0 {
				break
			}
			at := from + rel
			if !policyTermNegated(text, at) {
				return true
			}
			from = at + len(term)
		}
	}
	return false
}

func policyTermNegated(text string, at int) bool {
	if at <= 0 {
		return false
	}
	prefix := text[:at]
	start := 0
	for _, marker := range []string{"。", "；", ";", "\n", "！", "!", "？", "?", "但是", "不过", "但", " however ", " but "} {
		if i := strings.LastIndex(prefix, marker); i >= start {
			start = i + len(marker)
		}
	}
	scope := prefix[start:]
	return containsPolicyTerm(scope,
		"不得", "禁止", "不允许", "不要", "不可", "不能", "无需", "无须",
		"do not", "don't", "must not", "without", "not allowed", "never use")
}

func (p executionPolicy) allowsTool(tool agent.BaseTool) bool {
	if tool == nil {
		return false
	}
	name, group := tool.Name(), toolGroup(tool)
	if p.StrictSources {
		if name == "browser" || name == "run_agent" {
			return true
		}
		if !p.SourceVerificationOnly {
			switch group {
			case "file", "artifact", "util", "office", "skill":
				return true
			}
		}
		return false
	}
	if p.SourceVerificationOnly {
		switch {
		case group == "browser" || group == "gui_agent" || group == "web":
			return true
		case name == "search_evidence" || name == "file_read" || name == "current_time":
			return true
		default:
			return false
		}
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

func filterToolsForRequest(ctx context.Context, tools []agent.BaseTool, req ChatRunRequest) []agent.BaseTool {
	tools = filterEnabled(tools, req.EnabledTools)
	if req.executionPolicy != nil {
		tools = filterToolsByPolicy(tools, *req.executionPolicy)
		if selected, ok := modelprofile.FromContext(ctx); !ok || !selected.Capabilities.Vision {
			out := tools[:0]
			for _, tool := range tools {
				if tool != nil && tool.Name() != "run_agent" {
					out = append(out, tool)
				}
			}
			tools = out
		}
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
		rules = append(rules, "This is a source-verification task. Verify source, date and coverage, then answer directly. Do not create scripts, tests, build steps or acceptance files unless the user explicitly asks for a software or data artifact. After a browser navigation timeout or unknown outcome, inspect the current page once; if the target is absent, report the site as unreachable and do not reopen the same host.")
	}
	if p.UseDeepAgent {
		rules = append(rules, "Use at most three independent workers. Launch independent branches together, then synthesize their receipts without repeating their research.")
	}
	if len(rules) == 0 {
		return base
	}
	return strings.TrimSpace(base) + "\n\n[Execution policy]\n" + strings.Join(rules, "\n")
}

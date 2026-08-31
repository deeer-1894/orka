package service

import (
	"context"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
)

// fast_path.go — answer a question that needs no tools without building an agent.
//
// Measured on the same question ("explain idempotence in one sentence"):
//
//	through the agent   14.0s   3,733 tokens
//	direct model call    2.6s     651 tokens
//
// Five times slower and six times more expensive, for a question that used no
// tools at all. And this is not an edge case: 28% of all successful runs here
// made ZERO tool calls, another 19% made exactly one.
//
// The cost is the scaffolding, not the loop — tool schemas, sub-agent
// definitions and the ReAct framing are shipped on every turn whether or not
// anything is called. Switching model tiers does not help: the fast tier
// measured 16s against the strong tier's 14s on this same question, because the
// floor is the endpoint's round-trip, not the model.
//
// The hard part is knowing in advance. Trying the direct call and falling back
// is not enough on its own — 72% of runs would pay for a wasted attempt, which
// cancels most of the gain. So two things work together:
//
//  1. a free heuristic decides whether the direct path is even worth trying, so
//     the wasted-attempt rate stays low; and
//  2. the direct call gets an escape hatch — if the model finds it needs tools,
//     it says so and the full agent runs.
//
// The escape hatch is what makes this safe. Answering slowly is a nuisance;
// answering without the tools the question needed is wrong, and wrong is worse.

// needToolsMarker is what the model replies when the direct path cannot serve
// the question. Distinctive enough that it cannot appear in a real answer.
const needToolsMarker = "NEED_TOOLS"

// toolishMarkers are signals that a request almost certainly needs tools. Each
// is a reason to skip the direct attempt entirely rather than pay for a refusal.
var toolishMarkers = []string{
	// workspace and files
	"文件", "工作区", "目录", "保存", "写入", "读取", "存成", "创建", "删除",
	".txt", ".md", ".csv", ".json", ".pdf", ".png", ".xlsx", ".py",
	// network and retrieval
	"搜索", "查一下", "查询", "联网", "网页", "抓取", "下载", "最新", "今天", "现在",
	"http://", "https://", "www.",
	// execution and multi-step work
	"运行", "执行", "跑一下", "命令", "脚本", "调研", "报告", "对比", "生成", "画",
	"file", "search", "download", "fetch", "run ", "execute", "browse",
}

// fastPathPromptLimit is the length past which a request is assumed to be real
// work rather than a question. Kept low: the direct path should claim only the
// clear cases.
const fastPathPromptLimit = 120

// fastPathEligible reports whether a request is worth attempting without tools.
// Pure string inspection — it must never cost a model call, since avoiding one
// is the entire point.
func fastPathEligible(req ChatRunRequest) bool {
	// Anything that carries state or intent beyond the question itself goes
	// straight to the agent: attachments to read, a skill to apply, a paused run
	// to resume, or a workflow/schedule whose shape is already decided.
	if len(req.FileIDs) > 0 || req.ActiveSkill != "" || req.ResumeKey != "" ||
		req.resumeFrom != nil || req.TaskID != "" {
		return false
	}
	if req.Trigger != "" && req.Trigger != "manual" {
		return false
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" || len([]rune(msg)) > fastPathPromptLimit {
		return false
	}
	low := strings.ToLower(msg)
	for _, m := range toolishMarkers {
		if strings.Contains(low, m) {
			return false
		}
	}
	return true
}

// fastPathInstruction is appended to the system prompt for the direct attempt.
// Deliberately blunt about the escape hatch: a model that answers a question it
// cannot actually answer is the one failure mode that matters here.
const fastPathInstruction = "\n\n[本次为快速回答模式,你没有任何工具可用]\n" +
	"如果这个问题可以完全基于你自己的知识回答,直接回答它。\n" +
	"如果回答它需要读写文件、联网搜索、执行命令、查看工作区,或任何实时/私有信息," +
	"就只回复一个词:" + needToolsMarker + "(不要解释,不要尝试作答)。\n" +
	"宁可回复 " + needToolsMarker + ",也不要凭猜测作答。"

// tryFastPath attempts a direct, tool-free answer. Reports whether it served the
// request; false means the caller must run the full agent.
//
// On success it emits the same events an agent run would, so the UI, the
// persisted transcript and the conversation digest are all unchanged — the only
// difference is that no agent was built.
func (s *ChatService) tryFastPath(ctx context.Context, rc *agent.RunContext, req ChatRunRequest, client llm.Client, model string, raw func(messages.Message)) bool {
	if client == nil || s.DisableFastPath || !fastPathEligible(req) {
		return false
	}
	system := middlewares.DefaultSystemPrompt + fastPathInstruction
	msgs := make([]llm.ChatMessage, 0, len(rc.Messages)+1)
	msgs = append(msgs, llm.ChatMessage{Role: llm.RoleSystem, Content: system})
	for _, m := range rc.Messages {
		if m.Type != messages.EventChat {
			continue
		}
		switch m.Role {
		case messages.RoleUser:
			msgs = append(msgs, llm.ChatMessage{Role: llm.RoleUser, Content: m.Content})
		case messages.RoleAssistant:
			msgs = append(msgs, llm.ChatMessage{Role: llm.RoleAssistant, Content: m.Content})
		}
	}

	// Stream when the client supports it, so a fast answer also FEELS fast.
	// Deltas are buffered rather than emitted live: the reply may turn out to be
	// the escape marker, and streaming "NEED_TOOLS" to the user before falling
	// back to the agent would be worse than a moment's silence.
	var resp llm.Response
	var err error
	r := llm.Request{Model: model, Messages: msgs}
	if sc, okStream := client.(llm.StreamingClient); okStream {
		resp, err = sc.ChatStream(ctx, r, func(string) {})
	} else {
		resp, err = client.Chat(ctx, r)
	}
	if err != nil {
		return false // any trouble → the agent path, which has retry and failover
	}
	answer := strings.TrimSpace(resp.Content)
	if answer == "" || len(resp.ToolCalls) > 0 || strings.Contains(answer, needToolsMarker) {
		return false
	}

	// Serve it exactly as the agent path would.
	rc.Messages = append(rc.Messages, messages.Chat(messages.RoleAssistant, answer, rc.Meta))
	s.Msg.Deliver(rc, raw, messages.Chat(messages.RoleAssistant, answer, rc.Meta), true)
	middlewares.SetFinal(rc, answer)
	rc.Put(middlewares.VarRunTokens, resp.Usage.TotalTokens)
	rc.Put(middlewares.VarRunTools, 0)
	return true
}

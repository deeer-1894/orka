package service

import (
	"context"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_control_layer/service/middlewares"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/messages"
)

// The load-bearing direction. Answering slowly is a nuisance; answering without
// the tools the question needed is wrong, and this heuristic is the first of two
// defences against that (the model's escape hatch is the second).
func TestFastPathRejectsAnythingToolish(t *testing.T) {
	for _, msg := range []string{
		"读一下 notes.txt",
		"把结果保存到工作区",
		"搜索一下 Rust 的最新版本",
		"打开 https://example.com 看看",
		"运行 ls -la",
		"帮我生成一张图",
		"调研三个框架并对比",
		"现在几点",
		"list the files",
		"download the report",
	} {
		if fastPathEligible(ChatRunRequest{Message: msg}) {
			t.Errorf("would have answered without tools: %q", msg)
		}
	}
}

func TestFastPathAcceptsPlainQuestions(t *testing.T) {
	for _, msg := range []string{
		"用一句话解释什么是幂等性",
		"什么是 CAP 定理?",
		"Go 的 channel 和 mutex 有什么区别",
		"What is a monad?",
		"1+1 等于几",
	} {
		if !fastPathEligible(ChatRunRequest{Message: msg}) {
			t.Errorf("a plain question was sent to the agent: %q", msg)
		}
	}
}

// Anything carrying state or intent beyond the question itself belongs to the
// agent, whatever the wording looks like.
func TestFastPathRejectsStatefulRequests(t *testing.T) {
	base := "什么是幂等性"
	cases := map[string]ChatRunRequest{
		"attachment": {Message: base, FileIDs: []string{"a.pdf"}},
		"skill":      {Message: base, ActiveSkill: "researcher"},
		"resume key": {Message: base, ResumeKey: "k"},
		"run resume": {Message: base, resumeFrom: &runResume{}},
		"task":       {Message: base, TaskID: "t1"},
		"scheduled":  {Message: base, Trigger: "schedule"},
		"empty":      {Message: "   "},
		"long":       {Message: strings.Repeat("很长的问题", 40)},
	}
	for name, req := range cases {
		if fastPathEligible(req) {
			t.Errorf("%s took the fast path", name)
		}
	}
}

// A manual trigger is the ordinary chat case and must stay eligible.
func TestFastPathAllowsManualTrigger(t *testing.T) {
	for _, trig := range []string{"", "manual"} {
		if !fastPathEligible(ChatRunRequest{Message: "什么是幂等性", Trigger: trig}) {
			t.Errorf("trigger %q was excluded", trig)
		}
	}
}

func newFastPathService(t *testing.T, resp llm.Response) (*ChatService, *agent.RunContext, *collector) {
	t.Helper()
	svc, _ := testService(t, llm.NewMock(resp))
	svc.DisableFastPath = false // testService disables it; these tests are ABOUT it
	col := &collector{}
	rc := &agent.RunContext{Ctx: context.Background(), Vars: map[string]any{},
		Meta: messages.Meta{ConversationID: "c1"}}
	rc.Send = func(m messages.Message) { svc.Msg.Deliver(rc, col.sink, m, true) }
	return svc, rc, col
}

func TestFastPathServesAPlainAnswer(t *testing.T) {
	svc, rc, col := newFastPathService(t, llm.Response{Content: "幂等就是重复执行结果不变。", FinishReason: "stop"})
	req := ChatRunRequest{Message: "什么是幂等性", ConversationID: "c1"}

	if !svc.tryFastPath(context.Background(), rc, req, svc.Main, "m", col.sink) {
		t.Fatal("fast path declined a plain question")
	}
	if got := middlewares.Final(rc); !contains(got, "幂等") {
		t.Fatalf("final answer = %q", got)
	}
	chats := col.byType(messages.EventChat)
	if len(chats) == 0 || !contains(chats[len(chats)-1].Content, "幂等") {
		t.Fatal("the answer was not delivered to the stream")
	}
	if middlewares.RunTools(rc) != 0 {
		t.Fatal("recorded tool calls on a path that has no tools")
	}
}

// The escape hatch: when the model says it needs tools, the caller must run the
// full agent — and the user must never see the marker.
func TestFastPathBailsOutOnNeedTools(t *testing.T) {
	svc, rc, col := newFastPathService(t, llm.Response{Content: needToolsMarker, FinishReason: "stop"})
	req := ChatRunRequest{Message: "这个项目现在怎么样", ConversationID: "c1"}

	if svc.tryFastPath(context.Background(), rc, req, svc.Main, "m", col.sink) {
		t.Fatal("served an answer the model said it could not give")
	}
	for _, m := range col.byType(messages.EventChat) {
		if contains(m.Content, needToolsMarker) {
			t.Fatalf("leaked the escape marker to the user: %q", m.Content)
		}
	}
}

// A model that emits tool calls has told us it needs tools, whatever it wrote.
func TestFastPathBailsOutOnToolCalls(t *testing.T) {
	svc, rc, col := newFastPathService(t, llm.Response{
		Content:      "让我查一下",
		ToolCalls:    []llm.ToolCall{{ID: "1", Name: "web_search", Arguments: "{}"}},
		FinishReason: "tool_calls",
	})
	if svc.tryFastPath(context.Background(), rc, ChatRunRequest{Message: "什么是幂等性"}, svc.Main, "m", col.sink) {
		t.Fatal("served a turn that wanted to call a tool")
	}
}

func TestFastPathBailsOutOnEmptyAnswer(t *testing.T) {
	svc, rc, col := newFastPathService(t, llm.Response{Content: "   ", FinishReason: "stop"})
	if svc.tryFastPath(context.Background(), rc, ChatRunRequest{Message: "什么是幂等性"}, svc.Main, "m", col.sink) {
		t.Fatal("served an empty answer")
	}
}

func TestFastPathWithoutClientDeclines(t *testing.T) {
	svc, rc, col := newFastPathService(t, llm.Response{Content: "x", FinishReason: "stop"})
	if svc.tryFastPath(context.Background(), rc, ChatRunRequest{Message: "什么是幂等性"}, nil, "m", col.sink) {
		t.Fatal("served a request with no model configured")
	}
}

// The instruction has to actually tell the model how to bail out, or the escape
// hatch exists only in this file.
func TestFastPathInstructionExplainsTheEscape(t *testing.T) {
	for _, want := range []string{needToolsMarker, "联网", "文件", "不要凭猜测"} {
		if !contains(fastPathInstruction, want) {
			t.Errorf("instruction is missing %q", want)
		}
	}
}

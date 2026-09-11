package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
)

type gateScriptClient struct {
	requests []llm.Request
	respond  func(int, llm.Request) llm.Response
}

func (c *gateScriptClient) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	c.requests = append(c.requests, req)
	return c.respond(len(c.requests)-1, req), nil
}
func gateRequestHas(req llm.Request, name string) bool {
	for _, tool := range req.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
func gateCall(id, name, args string) llm.Response {
	return llm.Response{ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}, FinishReason: "tool_calls"}
}

// Use the advertised schema exactly as a model would. Before the fix, the
// general worker selects the parent's advertised task and aborts the entire run.
func TestDeepAgentCatalogAndActivationStayScoped(t *testing.T) {
	ctx := withToolGate(context.Background(), newToolGate())
	model := &gateScriptClient{respond: func(n int, req llm.Request) llm.Response {
		switch n {
		case 0, 4:
			return gateCall("delegate", "task", `{"subagent_type":"general-purpose","description":"generate a QR code"}`)
		case 1:
			if gateRequestHas(req, "task") {
				return gateCall("invalid-nesting", "task", `{"subagent_type":"researcher","description":"research"}`)
			}
			return gateCall("activate", "find_tools", `{"query":"qrcode"}`)
		case 2:
			return gateCall("generate", "qrcode", `{}`)
		default:
			return llm.Response{Content: "done", FinishReason: "stop"}
		}
	}}
	calls := 0
	tools := append(deepTestTools(), retrievalFixture{"qrcode", func(context.Context, map[string]any) (string, error) { calls++; return "QR generated", nil }})
	ag, err := BuildEinoDeepOrchestrator(ctx, model, "main", model, "mini", "delegate work", tools, nil, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RunEinoOnce(ctx, ag, "generate and verify"); err != nil {
		t.Fatalf("advertised tool crashed delegation: %v", err)
	}
	if len(model.requests) != 7 || calls != 1 {
		t.Fatalf("requests=%d qrcode calls=%d", len(model.requests), calls)
	}
	for _, i := range []int{0, 4, 6} {
		if !gateRequestHas(model.requests[i], "task") {
			t.Errorf("parent request %d lost task", i)
		}
	}
	for _, i := range []int{1, 2, 3, 5} {
		if gateRequestHas(model.requests[i], "task") {
			t.Errorf("worker request %d advertises unregistered task", i)
		}
	}
	if gateRequestHas(model.requests[1], "qrcode") {
		t.Error("hidden tool visible before activation")
	}
	for _, i := range []int{2, 4, 5} {
		if !gateRequestHas(model.requests[i], "qrcode") {
			t.Errorf("activation lost on request %d", i)
		}
	}
}

// One shared middleware instance is what DeepAgent uses for its parent and
// general worker. Activation must use the current invocation's own catalog.
func TestGateActivationCannotFindAnotherAgentsTool(t *testing.T) {
	root := newToolGate()
	mw := newGateMiddleware(root)
	parentCtx, _, err := mw.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{Tools: EinoTools([]agent.BaseTool{gateStubTool{name: "qrcode"}, gateStubTool{name: "slides"}})})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, _, err := mw.BeforeAgent(parentCtx, &adk.ChatModelAgentContext{Tools: EinoTools([]agent.BaseTool{gateStubTool{name: "qrcode"}})})
	if err != nil {
		t.Fatal(err)
	}
	parentGate, workerGate := toolGateFrom(parentCtx), toolGateFrom(workerCtx)
	if parentGate == nil || workerGate == nil {
		t.Fatal("agent execution context has no scoped gate")
	}
	if got := workerGate.search("slides"); len(got) != 0 {
		t.Fatal("worker can discover parent-only tool")
	}
	if _, err := (findTools{}).Invoke(workerCtx, map[string]any{"query": "qrcode"}); err != nil {
		t.Fatal(err)
	}
	if !names(parentGate.visible())["qrcode"] || !names(workerGate.visible())["qrcode"] {
		t.Fatal("activation is not shared across compatible catalogs")
	}
	if names(workerGate.visible())["slides"] {
		t.Fatal("parent-only tool leaked after activation")
	}
}

func TestSpecialistResearchGuidanceFollowsSharedAllowance(t *testing.T) {
	b := newRunBudget(20, 1000, 0)
	s := newResearchSession(newWorkspaceBackend(t.TempDir(), "reader"), ".orka_offload/specialist/evidence", b, 10)
	ctx := withResearchSession(withBudget(context.Background(), b), s)
	source := retrievalFixture{"fetch_url", func(context.Context, map[string]any) (string, error) {
		b.AddUsage(500, 0)
		return "URL: https://fixture.test/docs\nTitle: Checkpoints\n\nDurable checkpoints.", nil
	}}
	model := llm.NewMock(gateCall("fetch", "fetch_url", `{"url":"https://fixture.test/docs"}`), gateCall("lookup", "search_evidence", `{"query":"checkpoints"}`), llm.Response{Content: "done"})
	subs, err := BuildEinoSubAgents(ctx, model, "main", model, "mini", []agent.BaseTool{source, evidenceSearchTool{s}, gateStubTool{name: "file_write"}}, []config.SubAgentConfig{{Name: "researcher", Tools: []string{"fetch_url", "search_evidence", "file_write"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(ctx, subs[0], "research and write"); err != nil {
		t.Fatal(err)
	}
	if len(model.Requests) != 3 {
		t.Fatalf("model requests=%d", len(model.Requests))
	}
	if !gateRequestHas(model.Requests[0], "fetch_url") {
		t.Fatal("retrieval hidden before reserve")
	}
	for _, req := range model.Requests[1:] {
		if gateRequestHas(req, "fetch_url") {
			t.Error("specialist advertises retrieval after reserve")
		}
		if !gateRequestHas(req, "search_evidence") || !gateRequestHas(req, "file_write") {
			t.Error("evidence/delivery tools were removed")
		}
		live := 0
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, "[Live execution state]") {
				live++
				if !strings.Contains(msg.Content, "retrieval budget") || !strings.Contains(msg.Content, "Source catalog") {
					t.Error("guidance lost allowance or evidence catalog")
				}
			}
		}
		if live != 1 {
			t.Errorf("live state messages=%d, want one", live)
		}
	}
}

func TestSpecialistFinalBudgetNoticeRemainsLast(t *testing.T) {
	b := newRunBudget(20, 1000, 0)
	s := newResearchSession(nil, "", b, 10)
	ctx := withResearchSession(withBudget(context.Background(), b), s)
	model := llm.NewMock(gateCall("read", "file_read", `{"path":"report.md"}`), llm.Response{Content: "partial"})
	subs, err := BuildEinoSubAgents(ctx, model, "main", model, "mini", []agent.BaseTool{gateStubTool{name: "file_read"}}, []config.SubAgentConfig{{Name: "writer", Tools: []string{"file_read"}, MaxIters: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunEinoOnce(ctx, subs[0], "verify"); err != nil {
		t.Fatal(err)
	}
	req := model.Requests[len(model.Requests)-1]
	if len(req.Tools) != 0 {
		t.Fatal("delegate budget restored tools")
	}
	if !strings.Contains(req.Messages[len(req.Messages)-1].Content, "这是最后一次回复") {
		t.Fatal("research guidance overrides final delegate budget notice")
	}
}

func TestConcurrentAgentCatalogsShareOnlyActivation(t *testing.T) {
	root := newToolGate()
	for _, name := range []string{"qrcode", "slides", "csv_join", "pdf_extract"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := withToolGate(context.Background(), root)
			model := llm.NewMock(gateCall("activate", "find_tools", `{"query":"`+name+`"}`), gateCall("use", name, `{}`), llm.Response{Content: "done"})
			ag, err := BuildEinoAgent(ctx, model, "main", "use the requested tool", []agent.BaseTool{gateStubTool{name: name}}, 10, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RunEinoOnce(ctx, ag, name); err != nil {
				t.Fatal(err)
			}
			if len(model.Requests) != 3 || !gateRequestHas(model.Requests[1], name) {
				t.Fatal("activation lost in concurrent agent")
			}
			for _, req := range model.Requests {
				for _, tool := range req.Tools {
					if !coreTools[tool.Name] && tool.Name != name {
						t.Errorf("another agent's tool leaked: %s", tool.Name)
					}
				}
			}
		})
	}
}

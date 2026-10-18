package service

import (
	"context"
	"errors"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_control_layer/llm"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/messages"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/security"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalToolsIsolateConversations(t *testing.T) {
	base := t.TempDir()
	provider := LocalToolsProvider(base)
	for _, conv := range []string{"one", "two"} {
		ts, _, err := provider(context.Background(), ChatRunRequest{UserEmail: "u@example.com", ConversationID: conv})
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range ts {
			if tool.Name() == "file_write" {
				if _, err := tool.Invoke(context.Background(), map[string]any{"path": "report.txt", "content": conv}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, conv := range []string{"one", "two"} {
		b, err := os.ReadFile(filepath.Join(base, "u@example.com", "sessions", conv, "report.txt"))
		if err != nil || strings.TrimSpace(string(b)) != conv {
			t.Errorf("session %s file=%q err=%v", conv, b, err)
		}
	}
}

func TestSessionMissingContextNeverFallsBack(t *testing.T) {
	base := t.TempDir()
	if tools, _, err := LocalToolsProvider(base)(context.Background(), ChatRunRequest{UserEmail: "u"}); err == nil || len(tools) != 0 {
		t.Fatal("missing session created usable tools")
	}
	if be := newWorkspaceBackend(base, "u", ""); be != nil {
		t.Fatal("missing session enabled offload")
	}
}
func TestSessionAttachmentsAndDeliveryUseSameRoot(t *testing.T) {
	base := t.TempDir()
	svc := &ChatService{Cfg: &config.Config{}}
	svc.Cfg.Storage.BaseStoragePath = base
	for _, conv := range []string{"one", "two"} {
		root, _ := pathsafe.EnsureSession(base, "owner", conv)
		os.WriteFile(filepath.Join(root, "report.txt"), []byte(conv), 0644)
	}
	out := svc.processAttachments(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: "two", FileIDs: []string{"report.txt", "../one/report.txt"}})
	if !strings.Contains(out, "two") || strings.Contains(out, "one") {
		t.Fatalf("attachment scope: %s", out)
	}
}
func TestSessionQuantReportImportAndHarness(t *testing.T) {
	base := t.TempDir()
	svc := &ChatService{Cfg: &config.Config{}}
	svc.Cfg.Storage.BaseStoragePath = base
	root, _ := pathsafe.EnsureSession(base, "owner", "source")
	os.MkdirAll(filepath.Join(root, "reports"), 0755)
	os.WriteFile(filepath.Join(root, "reports", "input.md"), []byte("source report"), 0644)
	if got := svc.DiscoverReports("owner", "other"); len(got) != 0 {
		t.Fatal("report discovery crossed sessions")
	}
	if err := svc.copyPipelineReport(context.Background(), "owner", "source", "target", "reports/input.md"); err != nil {
		t.Fatal(err)
	}
	seedQuantAssets(base, "owner", "target")
	dest, _ := pathsafe.SessionRoot(base, "owner", "target")
	for _, p := range []string{"reports/input.md", "quant/backtest_runner.py"} {
		if _, err := os.Stat(filepath.Join(dest, p)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := agent.WithMeta(context.Background(), messages.Meta{UserEmail: "owner"})
	if _, err := (backtestTool{baseStorage: base}).Invoke(ctx, map[string]any{"expression": "rank(mom_20)"}); err == nil {
		t.Fatal("quant accepted missing session")
	}
}
func TestSessionArtifactLinks(t *testing.T) {
	blocks := []db.ArtifactBlock{{Type: "markdown", Data: map[string]any{"text": "[report](reports/a.csv) ![plot](plot.png)"}}}
	if err := scopeArtifactResources(blocks, "conv-one"); err != nil {
		t.Fatal(err)
	}
	text := blocks[0].Data["text"].(string)
	if strings.Count(text, "conv=conv-one") != 2 {
		t.Fatalf("unscoped links: %s", text)
	}
	for _, link := range []string{"../other/a.csv", "/api/v1/controller/file/download?conv=other&path=a.csv"} {
		b := []db.ArtifactBlock{{Type: "markdown", Data: map[string]any{"text": "[bad](" + link + ")"}}}
		if err := scopeArtifactResources(b, "conv-one"); err == nil {
			t.Fatalf("accepted %s", link)
		}
	}
}

func TestSessionMCPPoolBindsSignedConversation(t *testing.T) {
	type claimKey struct{}
	server := mcpserver.NewMCPServer("session-test", "1", mcpserver.WithToolCapabilities(true))
	server.AddTool(mcp.NewTool("session_probe"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		claims, _ := ctx.Value(claimKey{}).(security.ContextToken)
		return mcp.NewToolResultText(claims.UserEmail + ":" + claims.ConversationID), nil
	})
	endpoint := mcpserver.NewTestStreamableHTTPServer(server, mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
		claims, err := security.Verify(r.Header.Get("X-Orka-Token"), []byte("test-secret"))
		if err != nil {
			t.Error(err)
		}
		return context.WithValue(ctx, claimKey{}, claims)
	}))
	defer endpoint.Close()
	p := &mcpPool{mcpURL: endpoint.URL, secret: "test-secret", tokenTTL: time.Hour, maxAge: time.Hour, entries: map[string]*mcpEntry{}}
	defer p.closeAll()
	for _, conv := range []string{"one", "two", "one"} {
		tools, release, err := p.get(context.Background(), "owner", conv)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range tools {
			if tool.Name() == "session_probe" {
				out, err := tool.Invoke(context.Background(), nil)
				if err != nil || !strings.Contains(out, "owner:"+conv) {
					t.Fatalf("wrong token context: %s %v", out, err)
				}
			}
		}
		release()
	}
	if len(p.entries) != 2 {
		t.Fatalf("pool entries=%d", len(p.entries))
	}
	p.invalidate("owner")
	if len(p.entries) != 0 {
		t.Fatal("invalidation left session connections")
	}
}
func TestSessionRunDeliveryUsesActiveWorkspace(t *testing.T) {
	mock := llm.NewMock(llm.Response{ToolCalls: []llm.ToolCall{{ID: "probe", Name: "delivery_probe", Arguments: `{}`}}, FinishReason: "tool_calls"}, llm.Response{Content: "done", FinishReason: "stop"})
	svc, _ := testService(t, mock)
	base := t.TempDir()
	svc.Cfg.Storage.BaseStoragePath = base
	called := false
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return []agent.BaseTool{retrievalFixture{"delivery_probe", func(ctx context.Context, _ map[string]any) (string, error) {
			called = true
			d := deliveryFrom(ctx)
			if d == nil || d.root != filepath.Join(base, "owner", "sessions", "active") {
				t.Errorf("delivery root=%+v", d)
			}
			return "checked", nil
		}}}, nil, nil
	}
	svc.Run(context.Background(), ChatRunRequest{Message: "inspect delivery", UserEmail: "owner", ConversationID: "active"}, func(messages.Message) {})
	if !called {
		t.Fatal("delivery probe never invoked")
	}
}

func TestSessionConfirmationCannotApproveAnotherConversation(t *testing.T) {
	h := newConfirmHub()
	ch := h.register("action", "other", "shell")
	if h.resolve("action", true, true, "mine") {
		t.Fatal("approved another conversation")
	}
	select {
	case <-ch:
		t.Fatal("sent approval across conversations")
	default:
	}
	if !h.resolve("action", false, false, "other") {
		t.Fatal("authorized decision failed")
	}
	if <-ch {
		t.Fatal("rejection became approval")
	}
}

func TestSessionFastAnswerSurvivesToolProviderFailure(t *testing.T) {
	svc, _ := testService(t, llm.NewMock(llm.Response{Content: "42", FinishReason: "stop"}))
	svc.DisableFastPath = false
	svc.ToolsFor = func(context.Context, ChatRunRequest) ([]agent.BaseTool, func(), error) {
		return nil, nil, errors.New("gateway unavailable")
	}
	if status := svc.Run(context.Background(), ChatRunRequest{UserEmail: "owner", ConversationID: "one", Message: "1+1"}, func(messages.Message) {}); status != db.RunDone {
		t.Fatalf("successful tool-free answer marked %s after tool discovery failed", status)
	}
}

package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/orka-oss/orka_core/agent"
)

type providerBrowserStub struct{ invoked *bool }

func (providerBrowserStub) Name() string           { return "browser" }
func (providerBrowserStub) Group() string          { return "browser" }
func (providerBrowserStub) Description() string    { return "bounded DOM browser" }
func (providerBrowserStub) Schema() map[string]any { return map[string]any{"type": "object"} }
func (s providerBrowserStub) Invoke(context.Context, map[string]any) (string, error) {
	*s.invoked = true
	return "browser", nil
}

func TestBrowserProviderOptionsCatalogAndFallback(t *testing.T) {
	base := t.TempDir()
	invoked := false
	options := ToolsProviderOptions{Browser: providerBrowserStub{&invoked}}
	gateway := mcpserver.NewMCPServer("browser-catalog", "1", mcpserver.WithToolCapabilities(true))
	gateway.AddTool(mcp.NewTool("gateway_probe"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("catalog invoked gateway")
		return mcp.NewToolResultText("unexpected"), nil
	})
	endpoint := mcpserver.NewTestStreamableHTTPServer(gateway)
	defer endpoint.Close()
	for _, kind := range []string{"local", "mcp", "mcp-fallback"} {
		t.Run(kind, func(t *testing.T) {
			provider := LocalToolsProvider(base, options)
			if kind == "mcp-fallback" {
				provider, _ = MCPToolsProviderPooled(base, "bad://unavailable", "fake", time.Minute, nil, nil, options)
			}
			if kind == "mcp" {
				provider, _ = MCPToolsProviderPooled(base, endpoint.URL, "fake", time.Minute, nil, nil, options)
			}
			svc := &ChatService{ToolsFor: provider}
			count := 0
			for _, info := range svc.ToolCatalog(context.Background(), "owner") {
				if info.Name == "browser" {
					count++
					if info.Group != "browser" || !info.Danger {
						t.Fatalf("browser metadata: %+v", info)
					}
				}
			}
			if count != 1 || invoked {
				t.Fatalf("catalog count=%d invoked=%v", count, invoked)
			}
			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 0 {
				t.Fatalf("catalog created workspace: %v %v", entries, err)
			}
			ctx := context.WithValue(context.Background(), catalogContextKey{}, true)
			tools, cleanup, _ := provider(ctx, ChatRunRequest{UserEmail: "owner", EnabledTools: []string{"browser"}})
			if cleanup != nil {
				defer cleanup()
			}
			browser := false
			for _, tool := range tools {
				if tool.Name() == "browser" {
					browser = true
				}
				if tool.Name() == "run_agent" || tool.Name() == "shell" || tool.Name() == "python" {
					t.Fatalf("browser selection expanded capabilities: %s", tool.Name())
				}
			}
			if !browser {
				t.Fatal("browser selection lost built-in")
			}
			if requestedExecutionScope(withRequestedExecutionScope(ctx, ChatRunRequest{EnabledTools: []string{"browser"}})) {
				t.Fatal("DOM browser granted code execution")
			}
		})
	}
	var _ agent.BaseTool = providerBrowserStub{}
}

func TestBrowserProviderOptionsAreInstanceBound(t *testing.T) {
	a, b := false, false
	option := ToolsProviderOptions{Browser: providerBrowserStub{&a}}
	first := LocalToolsProvider(t.TempDir(), option)
	option.Browser = providerBrowserStub{&b}
	_ = LocalToolsProvider(t.TempDir(), option)
	ctx := context.WithValue(context.Background(), catalogContextKey{}, true)
	tools, _, err := first(ctx, ChatRunRequest{UserEmail: "owner", EnabledTools: []string{"browser"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name() == "browser" {
			tool.Invoke(ctx, nil)
		}
	}
	if !a || b {
		t.Fatal("another provider changed existing browser injection")
	}
}

func TestBrowserDelegateWorksWithDOMOnly(t *testing.T) {
	ctx := context.Background()
	atomic := []agent.BaseTool{gateStubTool{name: "browser"}}
	subs, err := BuildEinoSubAgents(ctx, nil, "fixture", atomic, nil)
	if err != nil || len(subs) != 1 || subs[0].Name(ctx) != "browser" {
		t.Fatalf("DOM-only deep delegate missing: %v %v", subs, err)
	}
}

func TestBrowserLegacyDelegatesDoNotShadowAtomicTools(t *testing.T) {
	ctx := context.Background()
	for _, dom := range []bool{false, true} {
		atomic := []agent.BaseTool{gateStubTool{name: "run_agent"}}
		want := "browser"
		if dom {
			atomic = append(atomic, gateStubTool{name: "browser"})
			want = "delegate_browser"
		}
		delegates, err := BuildEinoSubAgentTools(ctx, nil, "fixture", atomic, nil)
		if err != nil || len(delegates) != 1 {
			t.Fatalf("legacy delegates: %v %v", delegates, err)
		}
		info, err := delegates[0].Info(ctx)
		if err != nil || info.Name != want {
			t.Fatalf("legacy delegate name: %v %v, want %s", info, err, want)
		}
		if _, err := BuildEinoOrchestrator(ctx, nil, "fixture", "fixture", atomic, nil, 3, false); err != nil {
			t.Fatalf("legacy orchestrator DOM=%v: %v", dom, err)
		}
	}
}

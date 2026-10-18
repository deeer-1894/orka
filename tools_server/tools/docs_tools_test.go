package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/orka-oss/tools_server/identity"
)

func callDocsTool(t *testing.T, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.NewMCPServer("test", "1")
	Register(s, t.TempDir(), nil)
	tool := s.GetTool(name)
	if tool == nil {
		t.Fatalf("tool %s is not registered", name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	ctx := identity.With(context.Background(), identity.Identity{Scopes: []string{"web:search"}})
	res, err := tool.Handler(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			out.WriteString(tc.Text)
		}
	}
	return res, out.String()
}

func docsFixture(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestDocsToolsScopesAndBlacklist(t *testing.T) {
	for _, name := range []string{"discover_docs", "read_section"} {
		meta := Registry()[name]
		if meta.Group != "web" || meta.Scope != "web:search" {
			t.Errorf("%s has wrong metadata: %+v", name, meta)
		}
		s := mcpserver.NewMCPServer("test", "1")
		Register(s, t.TempDir(), nil)
		tool := s.GetTool(name)
		if tool == nil {
			t.Errorf("%s not registered", name)
			continue
		}
		res, err := tool.Handler(context.Background(), mcp.CallToolRequest{})
		if err != nil || !res.IsError {
			t.Errorf("%s allowed unscoped call", name)
		}
		blocked := mcpserver.NewMCPServer("test", "1")
		Register(blocked, t.TempDir(), map[string]bool{name: true})
		if blocked.GetTool(name) != nil {
			t.Errorf("%s ignored blacklist", name)
		}
	}
}

func TestDocsInvalidURLs(t *testing.T) {
	for _, name := range []string{"discover_docs", "read_section", "fetch_url"} {
		for _, u := range []string{"", "http://", "http://[bad", "file:///etc/passwd", "https://user:pass@example.com", "https://example.com:99999", "https://example.com/%zz"} {
			t.Run(name+"/"+u, func(t *testing.T) {
				res, out := callDocsTool(t, name, map[string]any{"url": u, "query": "retry"})
				if !res.IsError || out == "" {
					t.Errorf("expected useful URL error: %q", out)
				}
			})
		}
	}
}

func TestDocsHTTPBodyFailures(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte("<h1>retry</h1>incomplete"))
	}))
	defer s.Close()
	for _, name := range []string{"discover_docs", "read_section", "fetch_url"} {
		res, out := callDocsTool(t, name, map[string]any{"url": s.URL, "query": "retry"})
		if !res.IsError || !strings.Contains(out, "unexpected EOF") {
			t.Errorf("%s hid body read error: %q", name, out)
		}
	}
}

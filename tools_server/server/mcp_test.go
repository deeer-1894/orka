package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/orka-oss/orka_core/security"
)

func transportHeaders(h map[string]string) []transport.StreamableHTTPCOption {
	if len(h) == 0 {
		return nil
	}
	return []transport.StreamableHTTPCOption{transport.WithHTTPHeaders(h)}
}

const testSecret = "test-secret"

func startGateway(t *testing.T, cfg Config) string {
	t.Helper()
	gw := New(cfg)
	ts := mcpserver.NewTestStreamableHTTPServer(gw,
		mcpserver.WithHTTPContextFunc(ContextFunc([]byte(testSecret))),
	)
	t.Cleanup(ts.Close)
	return ts.URL
}

func connect(t *testing.T, url string, headers map[string]string) *mcpclient.Client {
	t.Helper()
	c, err := mcpclient.NewStreamableHttpClient(url, transportHeaders(headers)...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatal(err)
	}
	return c
}

func listNames(t *testing.T, c *mcpclient.Client) map[string]bool {
	t.Helper()
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tl := range res.Tools {
		out[tl.Name] = true
	}
	return out
}

func callText(t *testing.T, c *mcpclient.Client, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	txt := ""
	for _, ct := range res.Content {
		if tc, ok := ct.(mcp.TextContent); ok {
			txt += tc.Text
		}
	}
	return res, txt
}

func tokenHeader(t *testing.T, email string, scopes []string) map[string]string {
	claims := security.NewToken(email, scopes, time.Hour)
	claims.ConversationID = "test-session"
	tok, err := security.Sign(claims, []byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"X-Orka-Token": tok}
}

func TestGateway_FileWriteReadWithinRoot(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	c := connect(t, url, tokenHeader(t, "u@x.com", []string{"file:read", "file:write"}))

	names := listNames(t, c)
	if !names["file_read"] || !names["file_write"] || !names["file_list"] {
		t.Fatalf("file tools not listed for scoped user: %v", names)
	}

	if _, txt := callText(t, c, "file_write", map[string]any{"path": "notes/a.txt", "content": "hi"}); txt == "" {
		t.Fatal("file_write returned empty")
	}
	// actually on disk under base/u@x.com
	if b, err := os.ReadFile(filepath.Join(base, "u@x.com", "sessions", "test-session", "notes/a.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("disk content = %q err=%v", b, err)
	}
	if _, txt := callText(t, c, "file_read", map[string]any{"path": "notes/a.txt"}); txt != "hi" {
		t.Fatalf("file_read = %q", txt)
	}
}

func TestGateway_TraversalRejected(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	c := connect(t, url, tokenHeader(t, "u@x.com", []string{"file:write"}))

	res, txt := callText(t, c, "file_write", map[string]any{"path": "../../etc/evil", "content": "x"})
	if !res.IsError {
		t.Fatalf("traversal write should be an error result, got %q", txt)
	}
}

func TestGateway_NoTokenHidesAndDeniesScopedTools(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	c := connect(t, url, nil) // no token

	names := listNames(t, c)
	if names["file_write"] || names["file_read"] {
		t.Fatalf("scoped tools must be hidden without a token: %v", names)
	}
	// calling directly is still denied by the handler guard
	res, _ := callText(t, c, "file_write", map[string]any{"path": "a.txt", "content": "x"})
	if !res.IsError {
		t.Fatal("file_write without scope must be denied")
	}
}

func TestGateway_Blacklist(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base, Blacklist: []string{"file_list"}})
	c := connect(t, url, tokenHeader(t, "u@x.com", []string{"file:read", "file:write"}))

	names := listNames(t, c)
	if names["file_list"] {
		t.Fatalf("blacklisted tool must not be listed: %v", names)
	}
	if !names["file_read"] {
		t.Fatalf("non-blacklisted tool should remain: %v", names)
	}
}

func TestGateway_RBACScopeFilter(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	// only read scope -> write hidden, read visible
	c := connect(t, url, tokenHeader(t, "u@x.com", []string{"file:read"}))
	names := listNames(t, c)
	if names["file_write"] {
		t.Fatalf("file_write must be hidden without file:write scope: %v", names)
	}
	if !names["file_read"] {
		t.Fatalf("file_read should be visible with file:read scope: %v", names)
	}
}

func sessionHeader(t *testing.T, owner, conv string) map[string]string {
	claims := security.NewToken(owner, []string{"file:read", "file:write"}, time.Hour)
	claims.ConversationID = conv
	tok, err := security.Sign(claims, []byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"X-Orka-Token": tok}
}
func TestGatewaySessionFilesShellPython(t *testing.T) {
	t.Setenv("SHELL_TOOL", "1")
	base := t.TempDir()
	endpoint := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	for _, conv := range []string{"one", "two"} {
		c := connect(t, endpoint, sessionHeader(t, "owner", conv))
		defer c.Close()
		if res, txt := callText(t, c, "file_write", map[string]any{"path": "report.txt", "content": conv}); res.IsError {
			t.Fatal(txt)
		}
		if _, txt := callText(t, c, "shell", map[string]any{"command": `pwd; printf '%s' "$HOME"`}); !strings.Contains(txt, filepath.Join(base, "owner", "sessions", conv)) {
			t.Fatal(txt)
		}
		if _, err := exec.LookPath("python3"); err == nil {
			if res, txt := callText(t, c, "python", map[string]any{"code": "import os; open('python.txt','w').write(os.getcwd())"}); res.IsError {
				t.Fatal(txt)
			}
			b, err := os.ReadFile(filepath.Join(base, "owner", "sessions", conv, "python.txt"))
			if err != nil || string(b) != filepath.Join(base, "owner", "sessions", conv) {
				t.Fatalf("python wrote wrong workspace: %s %v", b, err)
			}
		}
		if _, txt := callText(t, c, "file_read", map[string]any{"path": "report.txt"}); txt != conv {
			t.Fatalf("session=%s got=%s", conv, txt)
		}
		if res, _ := callText(t, c, "file_read", map[string]any{"path": "../other/report.txt"}); !res.IsError {
			t.Fatal("traversal accepted")
		}
	}
	for _, headers := range []map[string]string{{"X-User-Email": "owner", "X-Conversation-ID": "one"}, sessionHeader(t, "owner", "")} {
		c := connect(t, endpoint, headers)
		defer c.Close()
		for _, name := range []string{"file_read", "python", "shell"} {
			res, txt := callText(t, c, name, map[string]any{"path": "report.txt", "code": "print('bad')", "command": "pwd"})
			if !res.IsError {
				t.Fatalf("missing verified session accepted %s: %s", name, txt)
			}
		}
	}
}

func TestGateway_FileWriteIntentSchemaAndModes(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	c := connect(t, url, tokenHeader(t, "u@x.com", []string{"file:read", "file:write"}))
	listed, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Tools {
		if tool.Name != "file_write" {
			continue
		}
		found = true
		mode, ok := tool.InputSchema.Properties["mode"].(map[string]any)
		if !ok {
			t.Errorf("missing mode schema: %+v", tool.InputSchema)
			continue
		}
		if mode["default"] != "create" {
			t.Errorf("wrong default: %+v", mode)
		}
		enum, ok := mode["enum"].([]any)
		if !ok || len(enum) != 3 || enum[0] != "create" || enum[1] != "replace" || enum[2] != "append" {
			t.Errorf("wrong enum: %+v", mode)
		}
		for _, word := range []string{"create", "append", "replace", "existing"} {
			if !strings.Contains(tool.Description, word) {
				t.Errorf("description omits %q", word)
			}
		}
	}
	if !found {
		t.Fatal("file_write not listed")
	}
	for _, step := range []struct {
		mode, content, want string
		reject              bool
	}{
		{"", "original", "original", false},
		{"", "supplement", "original", true},
		{"append", "+added", "original+added", false},
		{"replace", "complete replacement", "complete replacement", false},
	} {
		args := map[string]any{"path": "record.txt", "content": step.content}
		if step.mode != "" {
			args["mode"] = step.mode
		}
		result, text := callText(t, c, "file_write", args)
		if result.IsError != step.reject {
			t.Fatalf("unexpected outcome: %s", text)
		}
		if _, text := callText(t, c, "file_read", map[string]any{"path": "record.txt"}); text != step.want {
			t.Fatalf("wrong bytes: %q, want %q", text, step.want)
		}
	}
}

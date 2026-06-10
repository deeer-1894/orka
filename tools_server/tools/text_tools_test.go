package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callText(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestBase64RoundTrip(t *testing.T) {
	enc := callText(t, base64Tool(), map[string]any{"text": "hello 世界", "mode": "encode"})
	dec := callText(t, base64Tool(), map[string]any{"text": enc, "mode": "decode"})
	if dec != "hello 世界" {
		t.Errorf("round-trip failed: %q", dec)
	}
}

func TestHash(t *testing.T) {
	// sha256("abc") known vector
	got := callText(t, hashTool(), map[string]any{"text": "abc", "algo": "sha256"})
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("sha256(abc) = %q", got)
	}
}

func TestUUIDFormat(t *testing.T) {
	u := callText(t, uuidTool(), nil)
	if len(u) != 36 || strings.Count(u, "-") != 4 || u[14] != '4' {
		t.Errorf("not a v4 uuid: %q", u)
	}
}

func TestJSONFormat(t *testing.T) {
	out := callText(t, jsonFormatTool(), map[string]any{"json": `{"b":1,"a":2}`, "mode": "minify"})
	if out != `{"a":2,"b":1}` {
		t.Errorf("minify = %q", out)
	}
	pretty := callText(t, jsonFormatTool(), map[string]any{"json": `{"a":1}`})
	if !strings.Contains(pretty, "\n  \"a\": 1") {
		t.Errorf("pretty = %q", pretty)
	}
}

func TestTextStats(t *testing.T) {
	out := callText(t, textStatsTool(), map[string]any{"text": "hello 世界\nsecond line"})
	if !strings.Contains(out, "CJK characters: 2") || !strings.Contains(out, "lines: 2") {
		t.Errorf("stats = %q", out)
	}
}

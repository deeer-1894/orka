package tools

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callWith(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

// Every alias here cost a real run before it was accepted. The one that
// prompted this test: a survey wrote three findings files correctly with
// "path", slipped to "filename" on the fourth, and never recovered.
func TestPathArgAcceptsTheSynonymsModelsReachFor(t *testing.T) {
	for _, key := range []string{"path", "file", "filename", "file_path", "filepath"} {
		got := pathArg(callWith(map[string]any{key: "findings/eino.md", "content": "x"}))
		if got != "findings/eino.md" {
			t.Errorf("%q was not recognised as the path (got %q)", key, got)
		}
	}
}

// "path" stays authoritative when more than one is present, so a call that
// spells it correctly is never overridden by a stray alias.
func TestPathArgPrefersPath(t *testing.T) {
	got := pathArg(callWith(map[string]any{"filename": "wrong.md", "path": "right.md"}))
	if got != "right.md" {
		t.Fatalf("got %q, want right.md", got)
	}
}

func TestPathArgIgnoresBlanks(t *testing.T) {
	if got := pathArg(callWith(map[string]any{"path": "   ", "file": "real.md"})); got != "real.md" {
		t.Fatalf("a whitespace-only path was taken as real (got %q)", got)
	}
	if got := pathArg(callWith(map[string]any{})); got != "" {
		t.Fatalf("got %q from an empty call, want empty", got)
	}
}

// The expensive half of the bug was the ERROR. `open /workspace/...` never
// mentioned the argument, so the model had nothing to correct from and simply
// abandoned the file. The message has to name what it wanted and show what it
// received.
func TestMissingPathErrorNamesWhatWasSent(t *testing.T) {
	res := missingPathError(callWith(map[string]any{"fname": "x.md", "content": "y"}),
		`{"path": "notes/summary.md", "content": "..."}`)
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	for _, want := range []string{"path", "notes/summary.md", "You sent", "fname", "content"} {
		if !strings.Contains(text, want) {
			t.Errorf("error message omits %q: %s", want, text)
		}
	}
}

func TestMissingPathErrorWithNoArguments(t *testing.T) {
	res := missingPathError(callWith(nil), `{"path": "a.md"}`)
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "path is required") {
		t.Fatalf("unhelpful message with no arguments: %s", text)
	}
	if strings.Contains(text, "You sent") {
		t.Fatalf("dangled an empty list of received keys: %s", text)
	}
}

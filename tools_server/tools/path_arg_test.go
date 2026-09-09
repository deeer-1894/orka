package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/orka-oss/tools_server/identity"
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

// Since workspaces became per-conversation, EVERY conversation starts with no
// directory on disk — it is created by the first write. The first file_list of
// a new chat therefore hit ENOENT and came back as a tool error, which reads to
// the model as a broken tool rather than an empty workspace. It retried, then
// gave up on listing at all.
func TestListingAWorkspaceThatHasNoDirectoryYet(t *testing.T) {
	base := t.TempDir() // nothing created under it: a brand-new conversation
	ctx := identity.With(context.Background(), identity.Identity{
		Email: "u@x.com", Conversation: "conv-new", Scopes: []string{"*"},
	})
	res, err := fileList(base)(ctx, callWith(map[string]any{}))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("an empty workspace was reported as an error: %s", resultText(res))
	}
	if txt := resultText(res); !strings.Contains(txt, "empty") {
		t.Fatalf("listing said %q; it should say the workspace is empty", txt)
	}
}

// A missing SUBdirectory is still an error: there the model asked for something
// specific, and silently answering "empty" would hide its own typo.
func TestListingAMissingSubdirectoryIsStillAnError(t *testing.T) {
	base := t.TempDir()
	ctx := identity.With(context.Background(), identity.Identity{
		Email: "u@x.com", Conversation: "conv-new", Scopes: []string{"*"},
	})
	res, _ := fileList(base)(ctx, callWith(map[string]any{"path": "reports"}))
	if !res.IsError {
		t.Fatalf("a missing subdirectory was reported as success: %s", resultText(res))
	}
}

// Two conversations of one account must not see each other's files.
func TestFileToolsKeepConversationsApart(t *testing.T) {
	base := t.TempDir()
	ctxOf := func(conv string) context.Context {
		return identity.With(context.Background(), identity.Identity{
			Email: "u@x.com", Conversation: conv, Scopes: []string{"*"},
		})
	}
	if res, _ := fileWrite(base)(ctxOf("conv-a"), callWith(map[string]any{
		"path": "secret.md", "content": "only A",
	})); res.IsError {
		t.Fatalf("write failed: %s", resultText(res))
	}
	if res, _ := fileRead(base)(ctxOf("conv-a"), callWith(map[string]any{"path": "secret.md"})); res.IsError {
		t.Fatalf("the writing conversation cannot read its own file: %s", resultText(res))
	}
	if res, _ := fileRead(base)(ctxOf("conv-b"), callWith(map[string]any{"path": "secret.md"})); !res.IsError {
		t.Fatalf("another conversation read the file: %s", resultText(res))
	}
	if res, _ := fileList(base)(ctxOf("conv-b"), callWith(map[string]any{})); strings.Contains(resultText(res), "secret.md") {
		t.Fatalf("another conversation listed the file: %s", resultText(res))
	}
}

func resultText(res *mcp.CallToolResult) string {
	var s string
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			s += tc.Text
		}
	}
	return s
}

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// The probe's whole value is that its number is comparable with the threshold
// the reducer tests, so it has to reproduce eino's defaultTokenCounter: the
// rendered message over four BYTES. A "better" estimate here would be a worse
// diagnostic.
func TestProbeCountMatchesEinoAccounting(t *testing.T) {
	msgs := []*schema.Message{
		schema.UserMessage("hello"),
		{Role: schema.Assistant, Content: "hi", ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "fetch_url", Arguments: `{"url":"https://x"}`}},
		}},
		{Role: schema.Tool, ToolName: "fetch_url", Content: strings.Repeat("a", 4000)},
	}
	tok, count, biggest, biggestName := probeCount(msgs)

	if count != 3 {
		t.Fatalf("counted %d messages, want 3", count)
	}
	// user: "user\n\nhello\n" = 12 bytes -> 3
	// assistant: "assistant\n\nhi\n" (14) + "fetch_url\n" (10) + args (19) = 43 -> 10
	// tool: "tool\n\n" (6) + 4000 + "\n" = 4007 -> 1001
	if want := int64(3 + 10 + 1001); tok != want {
		t.Errorf("token estimate = %d, want %d (eino counts rendered bytes/4)", tok, want)
	}
	if biggest != 1001 || biggestName != "fetch_url" {
		t.Errorf("biggest tool msg = %d/%q, want 1001/fetch_url", biggest, biggestName)
	}
}

// A CJK-heavy context is further along than the byte arithmetic suggests, which
// is exactly the kind of thing a threshold tuned on English would miss. Pinned
// so the caveat in the probe's doc comment stays true.
func TestProbeUndercountsCJK(t *testing.T) {
	cjk, ascii := strings.Repeat("调", 1000), strings.Repeat("a", 1000)
	cjkTok, _, _, _ := probeCount([]*schema.Message{schema.UserMessage(cjk)})
	asciiTok, _, _, _ := probeCount([]*schema.Message{schema.UserMessage(ascii)})
	if cjkTok <= asciiTok {
		t.Fatalf("expected CJK to score higher per character in bytes (cjk=%d ascii=%d)", cjkTok, asciiTok)
	}
	// 3 bytes/char over 4 = 0.75 per character, against ~1 from a real tokenizer.
	if cjkTok > 800 {
		t.Errorf("CJK scored %d for 1000 characters; the 0.75/char undercount is the documented caveat", cjkTok)
	}
}

// The probe must be inert unless asked for: it logs once per model call.
func TestProbeOffByDefault(t *testing.T) {
	t.Setenv(ctxDebugEnv, "")
	if pre, post := ctxProbePair("orka"); pre != nil || post != nil {
		t.Fatal("probe must be off unless ORKA_CTX_DEBUG is set")
	}
	t.Setenv(ctxDebugEnv, "1")
	pre, post := ctxProbePair("orka")
	if pre == nil || post == nil {
		t.Fatal("ORKA_CTX_DEBUG=1 should install both halves")
	}
	// A post with no preceding pre must not report a bogus delta against zero.
	st := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("x")}}
	if _, _, err := post.BeforeModelRewriteState(context.Background(), st, nil); err != nil {
		t.Fatalf("post probe errored: %v", err)
	}
}

// The probe observes; it must never alter what the model receives.
func TestProbeDoesNotMutateState(t *testing.T) {
	t.Setenv(ctxDebugEnv, "1")
	pre, post := ctxProbePair("orka")
	msgs := []*schema.Message{schema.UserMessage("hello"), schema.AssistantMessage("hi", nil)}
	st := &adk.ChatModelAgentState{Messages: msgs}

	for _, mw := range []adk.ChatModelAgentMiddleware{pre, post} {
		_, out, err := mw.BeforeModelRewriteState(context.Background(), st, nil)
		if err != nil {
			t.Fatalf("probe errored: %v", err)
		}
		if len(out.Messages) != 2 || out.Messages[0].Content != "hello" {
			t.Fatalf("probe changed the message list: %+v", out.Messages)
		}
	}
}

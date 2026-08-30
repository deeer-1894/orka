package service

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/orka-oss/orka_control_layer/db"
)

func callAndResult(id, name, args, result string) []*schema.Message {
	a := schema.AssistantMessage("", nil)
	a.ToolCalls = []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: name, Arguments: args}}}
	return []*schema.Message{a, {Role: schema.Tool, ToolCallID: id, Content: result}}
}

// The load-bearing invariant: anything a later turn might cite as fact is
// derived mechanically. A model-paraphrased file path is worse than no path,
// because the next turn will act on it.
func TestDigestFactsAreMechanical(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, callAndResult("1", "file_write", `{"path":"reports/q3.md"}`, "wrote 812 bytes to reports/q3.md")...)
	msgs = append(msgs, callAndResult("2", "web_search", `{"q":"x"}`, strings.Repeat("噪音", 500))...)

	d := buildDigest("run_1", "写一份报告", msgs)
	if len(d.Facts) != 1 {
		t.Fatalf("facts = %v, want exactly the durable effect", d.Facts)
	}
	if !contains(d.Facts[0], "reports/q3.md") {
		t.Fatalf("fact lost the path: %q", d.Facts[0])
	}
	if d.Learned != "" {
		t.Fatal("buildDigest must not invent the model half; that is filled in asynchronously")
	}
	if d.Tools != 2 {
		t.Fatalf("tools = %d, want 2", d.Tools)
	}
}

// Retrieval output is what the model half is for; it must never be promoted
// into the citable half.
func TestDigestExcludesRetrievalFromFacts(t *testing.T) {
	msgs := callAndResult("1", "fetch_url", `{"url":"http://x"}`, "some long page body")
	d := buildDigest("run_1", "查一下", msgs)
	if len(d.Facts) != 0 {
		t.Fatalf("facts = %v, want none — fetch_url output is not a citable fact", d.Facts)
	}
}

func TestDigestCapsFactsWithCount(t *testing.T) {
	var msgs []*schema.Message
	for i := 0; i < digestMaxFacts+5; i++ {
		msgs = append(msgs, callAndResult(itoa(i), "file_write", `{"path":"f`+itoa(i)+`.txt"}`, "wrote 1 bytes")...)
	}
	d := buildDigest("run_1", "写文件", msgs)
	if len(d.Facts) != digestMaxFacts+1 {
		t.Fatalf("facts = %d, want %d capped entries plus one count line", len(d.Facts), digestMaxFacts+1)
	}
	if !contains(d.Facts[len(d.Facts)-1], "5") {
		t.Fatalf("overflow line does not report the remainder: %q", d.Facts[len(d.Facts)-1])
	}
}

// A tool that reports nothing useful must still yield a locatable fact.
func TestDescribeFactFallsBackToArgs(t *testing.T) {
	if got := describeFact("file_write", `{"path":"a/b.txt"}`, ""); !contains(got, "a/b.txt") {
		t.Fatalf("got %q, want the requested path", got)
	}
	if got := describeFact("file_write", "", "wrote 3 bytes to c.txt"); !contains(got, "c.txt") {
		t.Fatalf("got %q, want the result text", got)
	}
	// An oversized result must not be pasted whole into the preamble.
	long := strings.Repeat("x", 5000)
	if got := describeFact("artifact_publish", "", long); len(got) > 300 {
		t.Fatalf("fact line is %d chars; it would dominate the preamble", len(got))
	}
}

func TestDigestSourceSkipsFactsAndKeepsOrder(t *testing.T) {
	var msgs []*schema.Message
	msgs = append(msgs, callAndResult("1", "web_search", "{}", "第一个发现")...)
	msgs = append(msgs, callAndResult("2", "file_write", "{}", "wrote 1 bytes")...)
	msgs = append(msgs, callAndResult("3", "fetch_url", "{}", "第二个发现")...)

	src := digestSource(msgs)
	if contains(src, "wrote 1 bytes") {
		t.Fatal("file_write output reached the model input; it is already captured exactly")
	}
	i, j := strings.Index(src, "第一个发现"), strings.Index(src, "第二个发现")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("findings are out of chronological order: %q", src)
	}
}

func TestDigestSourceRespectsBudget(t *testing.T) {
	var msgs []*schema.Message
	for i := 0; i < 40; i++ {
		msgs = append(msgs, callAndResult(itoa(i), "fetch_url", "{}", strings.Repeat("y", 3000))...)
	}
	if got := len(digestSource(msgs)); got > digestSourceChars+2100 {
		t.Fatalf("source is %d chars, well past the %d budget", got, digestSourceChars)
	}
}

// The preamble is memory, not instruction. A conversation's own record must not
// be able to read as a new user command.
func TestDigestPreambleIsMarkedAsRecord(t *testing.T) {
	pre := digestPreamble([]db.RunDigest{
		{Prompt: "调研 A", Facts: []string{"file_write: a.md"}, Learned: "A 的结论"},
	})
	for _, want := range []string{"调研 A", "a.md", "A 的结论", "不是用户的新指令"} {
		if !contains(pre, want) {
			t.Errorf("preamble missing %q:\n%s", want, pre)
		}
	}
	// The provenance split has to survive into the text, or the caution is moot.
	if !contains(pre, "产出") || !contains(pre, "发现") {
		t.Errorf("preamble does not distinguish mechanical facts from model prose:\n%s", pre)
	}
}

// A first turn has nothing to recall and must not be given an empty preamble
// that reads as a claim about prior work.
func TestDigestPreambleEmpty(t *testing.T) {
	if got := digestPreamble(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestDigestPreambleTolerantOfPartialDigests(t *testing.T) {
	// The model half failing is an expected path: facts-only must still render.
	pre := digestPreamble([]db.RunDigest{{Prompt: "任务", Facts: []string{"file_write: x.md"}}})
	if !contains(pre, "x.md") {
		t.Fatalf("facts-only digest did not render:\n%s", pre)
	}
	if contains(pre, "发现:") {
		t.Fatalf("rendered an empty findings section:\n%s", pre)
	}
}

func TestBuildDigestEmptyTranscript(t *testing.T) {
	d := buildDigest("run_1", "hi", nil)
	if len(d.Facts) != 0 || d.Tools != 0 {
		t.Fatalf("empty transcript produced %+v", d)
	}
}

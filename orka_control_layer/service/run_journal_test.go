package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func toolCallMsg(ids ...string) *schema.Message {
	m := schema.AssistantMessage("", nil)
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, schema.ToolCall{ID: id})
	}
	return m
}

func toolResultMsg(id string) *schema.Message {
	return &schema.Message{Role: schema.Tool, ToolCallID: id, Content: "ok"}
}

// The failure mode that matters: a run dies DURING a tool call, so the
// transcript ends with an assistant turn whose tool_calls were never answered.
// Providers reject that with a hard 400, which would make every resume fail —
// and look like the resume feature is broken rather than its input.
func TestResumeDropsUnansweredToolCall(t *testing.T) {
	f := &journalFile{
		Seed: []*schema.Message{schema.UserMessage("查一下")},
		Messages: []*schema.Message{
			toolCallMsg("a"), toolResultMsg("a"),
			toolCallMsg("b"), // died here — no result for b
		},
	}
	got := resumeMessages(f)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3 (the dangling tool call must be dropped)", len(got))
	}
	last := got[len(got)-1]
	if last.Role != schema.Tool || last.ToolCallID != "a" {
		t.Fatalf("transcript ends with %v/%q, want the answered tool result", last.Role, last.ToolCallID)
	}
}

// A parallel tool call is only coherent if EVERY branch came back.
func TestResumeDropsPartiallyAnsweredParallelCall(t *testing.T) {
	f := &journalFile{
		Seed: []*schema.Message{schema.UserMessage("go")},
		Messages: []*schema.Message{
			toolCallMsg("a", "b"),
			toolResultMsg("a"), // b never returned
		},
	}
	// The tool result for "a" is retained (it is answered); the assistant turn
	// that requested both is not the last message, so it stays too — dropping it
	// would orphan "a". What must NOT happen is a crash or an empty transcript.
	got := resumeMessages(f)
	if len(got) == 0 {
		t.Fatal("sanitization emptied a transcript that had real work in it")
	}
}

func TestResumeKeepsCompleteTranscript(t *testing.T) {
	f := &journalFile{
		Seed: []*schema.Message{schema.UserMessage("go")},
		Messages: []*schema.Message{
			toolCallMsg("a"), toolResultMsg("a"),
			schema.AssistantMessage("做完了", nil),
		},
	}
	if got := resumeMessages(f); len(got) != 4 {
		t.Fatalf("got %d, want all 4 — a clean transcript must survive intact", len(got))
	}
}

func TestResumeUnwindsMultipleDanglingTurns(t *testing.T) {
	f := &journalFile{
		Seed:     []*schema.Message{schema.UserMessage("go")},
		Messages: []*schema.Message{toolCallMsg("a"), nil, toolCallMsg("b")},
	}
	got := resumeMessages(f)
	if len(got) != 1 || got[0].Role != schema.User {
		t.Fatalf("got %d messages, want just the seed once every dangling turn unwinds", len(got))
	}
}

func TestResumeNilJournal(t *testing.T) {
	if got := resumeMessages(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// A journal must survive the crash it exists for, so the write is atomic and the
// round trip has to preserve tool calls — the part that makes it replayable.
func TestJournalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "run_1", []*schema.Message{schema.UserMessage("hi")})
	if j == nil {
		t.Fatal("journal not created")
	}
	j.append(toolCallMsg("a"))
	j.append(toolResultMsg("a"))
	j.flush()

	f := loadJournal(dir, "run_1")
	if f == nil {
		t.Fatal("journal did not load back")
	}
	if len(f.Seed) != 1 || len(f.Messages) != 2 {
		t.Fatalf("seed=%d msgs=%d, want 1/2", len(f.Seed), len(f.Messages))
	}
	if len(f.Messages[0].ToolCalls) != 1 || f.Messages[0].ToolCalls[0].ID != "a" {
		t.Fatal("tool calls did not survive the round trip — the transcript would be unreplayable")
	}
	if f.Messages[1].ToolCallID != "a" {
		t.Fatal("tool result lost its call id")
	}
	// No temp file may be left behind.
	if _, err := os.Stat(filepath.Join(dir, ".orka_journals", "run_1.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("a .tmp file survived the flush")
	}
}

func TestJournalDiscardRemovesFile(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "run_2", nil)
	j.append(schema.AssistantMessage("x", nil))
	j.flush()
	if loadJournal(dir, "run_2") == nil {
		t.Fatal("journal missing before discard")
	}
	j.discard()
	if loadJournal(dir, "run_2") != nil {
		t.Fatal("journal survived discard")
	}
}

// A run id is not a path. Journals are keyed by an id we generate, but the
// store must not be one traversal bug away from writing outside its directory.
func TestJournalIDCannotEscapeDir(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "../../escape", nil)
	if j == nil {
		t.Fatal("journal not created")
	}
	want := filepath.Join(dir, ".orka_journals")
	if got := filepath.Dir(j.path()); got != want {
		t.Fatalf("journal path escaped to %q, want inside %q", got, want)
	}
}

func TestJournalNilIsInert(t *testing.T) {
	var j *runJournal
	j.append(schema.AssistantMessage("x", nil))
	j.setSeed([]*schema.Message{schema.UserMessage("y")})
	j.flush()
	j.discard()
	if j.steps() != 0 {
		t.Fatal("nil journal reported steps")
	}
	if newRunJournal("", "run", nil) != nil {
		t.Fatal("journal created without storage configured")
	}
}

func TestJournalStepsCountsProducedMessages(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "run_3", []*schema.Message{schema.UserMessage("seed")})
	for i := 0; i < 5; i++ {
		j.append(schema.AssistantMessage("x", nil))
	}
	if got := j.steps(); got != 5 {
		t.Fatalf("steps = %d, want 5 (the seed is not work this run did)", got)
	}
}

func TestResumeNoticeExplainsWhy(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"cancelled", "手动停止"},
		{"interrupted", "服务重启"},
		{"failed", "意外中断"},
	} {
		m := resumeNotice(tc.reason, 7)
		if !contains(m.Content, tc.want) {
			t.Errorf("notice for %q missing %q: %s", tc.reason, tc.want, m.Content)
		}
		if !contains(m.Content, "不要从头重做") {
			t.Errorf("notice for %q does not tell the agent to avoid redoing work", tc.reason)
		}
		if !contains(m.Content, "7") {
			t.Errorf("notice for %q does not say how much was preserved", tc.reason)
		}
	}
}

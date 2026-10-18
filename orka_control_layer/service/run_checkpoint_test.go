package service

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/schema"
	"github.com/orka-oss/orka_core/messages"
	"os"
	"strings"
	"testing"
)

func TestCheckpointRestoresRequirementsAndBudget(t *testing.T) {
	b := newRunBudget(100, 1000, 0)
	b.AddUsage(600, 100)
	p := &planTracker{}
	p.record([]messages.PlanStep{{Title: "Report", Status: "pending"}})
	d := newDeliveryTracker(t.TempDir())
	d.declare([]string{"report.md"})
	ctx := withDelivery(withPlanTracker(withBudget(context.Background(), b), p), d)
	dir := t.TempDir()
	j := newRunJournal(dir, "checkpoint", nil)
	j.trackState(ctx)
	j.append(schema.AssistantMessage("work", nil))
	j.flush()
	f := loadJournal(dir, "checkpoint")
	if f == nil || f.Checkpoint == nil {
		t.Fatal("missing durable execution state")
	}
	next := newRunBudget(100, 1000, 0)
	plan := &planTracker{}
	delivery := newDeliveryTracker(d.root)
	restoreCheckpoint(f.Checkpoint, next, plan, delivery)
	if next.totalSpentTokens() != 700 || next.spentTokens() != 0 {
		t.Fatalf("resume reset allowance or rebilled old usage: %+v", next)
	}
	if len(plan.unfinished()) != 1 || len(delivery.snapshot()) != 1 {
		t.Fatal("resume dropped requirements")
	}
	next.AddUsage(200, 100)
	if !next.observe(nil) || next.exhausted() != "tokens" {
		t.Fatal("resumed run exceeded original allowance")
	}
}

func TestCheckpointPreservesFailedBrowserEvidence(t *testing.T) {
	p := &planTracker{}
	p.record([]messages.PlanStep{{ID: "browser", Title: "返回浏览器页面并确认 URL", Status: "pending"}})
	callBrowserReceipt(t, p, "browser", map[string]any{"action": "open", "url": "https://example.com"}, `{"ok":false,"error":{"code":"timeout"}}`)
	ctx := withPlanTracker(context.Background(), p)
	checkpoint := checkpointFrom(ctx)
	restored := &planTracker{}
	restoreCheckpoint(checkpoint, nil, restored, nil)
	if len(restored.checkpoint().Browser) == 0 {
		t.Fatal("browser failure was lost across checkpoint")
	}
	if _, err := (planTool{}).Invoke(withPlanTracker(context.Background(), restored), map[string]any{
		"steps": []any{map[string]any{"id": "browser", "title": "返回浏览器页面并确认 URL", "status": "done"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(restored.unfinished()) != 1 {
		t.Fatal("checkpointed failed browser step was incorrectly closed")
	}
}
func TestJournalFlushRetriesAfterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "retry", nil)
	if err := os.Mkdir(j.path()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	j.append(schema.AssistantMessage("durable", nil))
	j.flush()
	os.Remove(j.path() + ".tmp")
	j.flush()
	if f := loadJournal(dir, "retry"); f == nil || len(f.Messages) != 1 {
		t.Fatal("failed flush lost dirty state")
	}
}
func TestResumeFillsUnknownParallelOutcomeWithoutRepeatingCompletedWork(t *testing.T) {
	f := &journalFile{Seed: []*schema.Message{schema.UserMessage("go")}, Messages: []*schema.Message{toolCallMsg("done", "unknown"), toolResultMsg("done")}}
	out := resumeMessages(f)
	results := map[string]string{}
	for _, m := range out {
		if m.Role == schema.Tool {
			results[m.ToolCallID] = m.Content
		}
	}
	if results["done"] != "ok" || !strings.Contains(results["unknown"], "unknown") {
		t.Fatalf("incoherent partial transcript: %+v", results)
	}
}

func TestJournalRefusesStaleDurabilityAfterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	j := newRunJournal(dir, "stale", nil)
	b := newRunBudget(100, 1000, 0)
	b.carried = 700
	j.trackState(withBudget(context.Background(), b))
	j.append(schema.AssistantMessage("previous work", nil))
	if !j.flush() {
		t.Fatal("initial flush failed")
	}
	b.AddUsage(200, 0)
	if err := os.Mkdir(j.path()+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if j.flush() {
		t.Fatal("failed latest write reported durable")
	}
	if f := loadJournal(dir, "stale"); f.Checkpoint.SpentTokens != 700 {
		t.Fatal("fixture should retain old checkpoint")
	}
	os.Remove(j.path() + ".tmp")
	if !j.flush() {
		t.Fatal("retry did not persist latest ledger")
	}
	if f := loadJournal(dir, "stale"); f.Checkpoint.SpentTokens != 900 {
		t.Fatal("retry lost accounted usage")
	}
}
func TestReaperRetentionIncludesInheritedObligations(t *testing.T) {
	f := &journalFile{Checkpoint: &runCheckpoint{SpentTokens: 700, Outputs: []string{"report.md"}}}
	if f.recoverableSteps() < resumeWorthwhileSteps {
		t.Fatal("crash reaper would discard inherited state")
	}
}

func TestRecoveredDelegateArchiveSurvivesNextJournal(t *testing.T) {
	dir := t.TempDir()
	old := newRunJournal(dir, "old", nil)
	for i := 0; i < 40; i++ {
		old.appendDelegate("worker", schema.AssistantMessage(strings.Repeat("evidence", 200), nil))
	}
	if !old.flush() {
		t.Fatal("initial archive write failed")
	}
	f := loadJournal(dir, "old")
	rr := resumeJournal(f)
	next := newRunJournal(dir, "next", nil)
	next.inherit(rr)
	if !next.flush() {
		t.Fatal("successor write failed")
	}
	old.discard()
	got := loadJournal(dir, "next")
	if len(got.Delegates) != 40 || got.Delegates[0].Message.Content != f.Delegates[0].Message.Content {
		t.Fatal("full delegate archive lost in handoff")
	}
}

func TestCheckpointRoundTripPreservesBrowserCausalityAndPartialVerification(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	for _, url := range []string{"https://example.com/a", "https://example.com/b"} {
		failed := recoveryCall(t, p, "open", url)
		p.completeBrowserCall(failed, `{"ok":false,"error":{"code":"timeout"}}`)
	}
	good := recoveryObservation(t, p, "open", "https://example.com/a")
	setRecoveryStep(p, "active", good.receipt.ID)
	recoveryCall(t, p, "open", "https://example.com/b") // interrupted during checkpoint
	data, err := json.Marshal(checkpointFrom(withPlanTracker(context.Background(), p)))
	if err != nil {
		t.Fatal(err)
	}
	var saved runCheckpoint
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	restored := &planTracker{}
	restoreCheckpoint(&saved, nil, restored, nil)
	state := restored.checkpoint().Browser["id:a"]
	if len(state.Pending) != 0 || len(state.Failures) != 2 {
		t.Fatalf("incorrect restored obligations: %+v", state)
	}
	for _, failure := range state.Failures {
		if failure.TargetURL != "https://example.com/b" {
			t.Fatal("resolved a was restored as a failure")
		}
	}
	setRecoveryStep(restored, "done", good.receipt.ID)
	if restored.completed() {
		t.Fatal("old observation cleared restored in-flight work")
	}
	fresh := recoveryObservation(t, restored, "open", "https://example.com/b")
	setRecoveryStep(restored, "done", fresh.receipt.ID)
	if !restored.completed() {
		t.Fatal("JSON checkpoint could not complete after fresh recovery")
	}
}

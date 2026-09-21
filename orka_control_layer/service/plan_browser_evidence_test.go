package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/orka-oss/orka_core/messages"
)

type evidenceBrowserTool struct {
	invoke func(context.Context, map[string]any) (string, error)
}

func (*evidenceBrowserTool) Name() string           { return "browser" }
func (*evidenceBrowserTool) Description() string    { return "test browser boundary" }
func (*evidenceBrowserTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *evidenceBrowserTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	return t.invoke(ctx, args)
}

func TestBrowserEvidenceAdapterBindsBeforeDispatchAndStripsMetadata(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	browserEvidenceStep(p, "b", "active")
	calls := 0
	base := &evidenceBrowserTool{invoke: func(_ context.Context, args map[string]any) (string, error) {
		calls++
		if _, exists := args["plan_step_id"]; exists {
			t.Fatal("plan metadata leaked into browser transport")
		}
		// A plan change during execution must not move this observation to b.
		browserEvidenceStep(p, "a", "pending")
		return `{"ok":true,"url":"https://example.com/","snapshot":{"text":"loaded"}}`, nil
	}}
	ctx := withPlanTracker(context.Background(), p)
	wrapped := EinoTool(base)
	if _, err := wrapped.Info(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := wrapped.InvokableRun(ctx, `{"action":"open","url":"https://example.com/"}`)
	if err != nil || calls != 0 || !strings.Contains(out, "plan_step_id") {
		t.Fatalf("ambiguous dispatch: %s %v", out, err)
	}
	out, err = wrapped.InvokableRun(ctx, `{"plan_step_id":"a","action":"open","url":"https://example.com/"}`)
	if err != nil || calls != 1 || !strings.Contains(out, `[Plan browser evidence] {"step":"a"`) {
		t.Fatalf("missing ownership: %s %v", out, err)
	}
	state := p.checkpoint().Browser
	if len(state["id:a"].Receipts) != 1 || len(state["id:b"].Receipts) != 0 {
		t.Fatal(state)
	}
}

func TestBrowserCanceledDispatchRequiresVerification(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	ctx, cancel := context.WithCancel(withPlanTracker(context.Background(), p))
	defer cancel()
	base := &evidenceBrowserTool{invoke: func(context.Context, map[string]any) (string, error) {
		cancel()
		return "", context.Canceled
	}}
	_, err := EinoTool(base).InvokableRun(ctx, `{"action":"click","ref":"r1"}`)
	if err != context.Canceled {
		t.Fatal(err)
	}
	state := p.checkpoint().Browser["id:a"]
	if len(state.Pending) != 0 || len(state.Failures) != 1 {
		t.Fatalf("lost uncertain operation: %+v", state)
	}
}

func TestBrowserRecoveryFollowsRequestedRedirect(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	browserEvidenceCall(p, "a", "open", "https://example.com/start", false)
	call := mustBeginBrowserCall(t, p, "a", map[string]any{"action": "open", "url": "https://example.com/start"})
	p.completeBrowserCall(call, `{"ok":true,"url":"https://example.com/final","snapshot":{"text":"redirected page"}}`)
	browserEvidenceStep(p, "a", "done", call.receipt.ID)
	if !p.completed() {
		t.Fatal(p.snapshot())
	}
}

func TestBrowserFailureKeepsNewestDispatchWhenResultsArriveOutOfOrder(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	first := mustBeginBrowserCall(t, p, "a", map[string]any{"action": "click"})
	second := mustBeginBrowserCall(t, p, "a", map[string]any{"action": "click"})
	p.completeBrowserCall(second, `{"ok":false,"error":{"code":"timeout"}}`)
	p.completeBrowserCall(first, `{"ok":false,"error":{"code":"timeout"}}`)
	for _, failure := range p.checkpoint().Browser["id:a"].Failures {
		if failure.Sequence != second.receipt.Sequence {
			t.Fatal("older result replaced newest failed dispatch")
		}
	}
}

func browserEvidenceStep(p *planTracker, id, status string, ids ...string) {
	p.record([]messages.PlanStep{{ID: id, Title: id, Status: status, Reason: "Observed the required destination and visible content", EvidenceIDs: ids}})
}

func TestBrowserOriginRecoveryPointsToExistingObservation(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "filter", "active")
	browserEvidenceCall(p, "filter", "open", "https://example.com", false)
	root := browserEvidenceCall(p, "filter", "snapshot", "https://example.com/", true)
	filtered := browserEvidenceCall(p, "filter", "click", "https://example.com/?query=PostgreSQL", true)
	browserEvidenceStep(p, "filter", "done", filtered)
	needs := p.browserRecoveryNeeds()
	if len(needs) != 1 || len(needs[0].Candidates) != 1 || needs[0].Candidates[0] != root {
		t.Fatalf("missing existing root recovery: %+v", needs)
	}
	browserEvidenceStep(p, "filter", "done", root, filtered)
	if !p.completed() || len(p.browserRecoveryNeeds()) != 0 {
		t.Fatal("origin slash required unnecessary repeat navigation")
	}
	for _, other := range []string{"https://example.com/other", "https://example.com/?query=other", "https://other.example/"} {
		if sameObservedURL("https://example.com", other) {
			t.Fatal("different destination accepted", other)
		}
	}
}
func browserEvidenceCall(p *planTracker, id, action, url string, ok bool) string {
	args := map[string]any{"action": action, "url": url}
	result := map[string]any{"ok": ok, "url": url, "snapshot": map[string]any{"text": "page state"}}
	if !ok {
		result["error"] = map[string]any{"code": "timeout"}
		delete(result, "snapshot")
	}
	b, _ := json.Marshal(result)
	call, err := p.beginBrowserCall(id, args)
	if err != nil {
		panic(err)
	}
	p.completeBrowserCall(call, string(b))
	state := p.checkpoint().Browser["id:"+id]
	return state.Receipts[len(state.Receipts)-1].ID
}
func TestBrowserPlanRecoveryRequiresNewEvidenceFromItsDestination(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "visit", "active")
	old := browserEvidenceCall(p, "visit", "open", "https://example.com/a", true)
	browserEvidenceCall(p, "visit", "open", "https://example.com/b", false)
	browserEvidenceStep(p, "visit", "done", old)
	if p.completed() {
		t.Fatal("old successful page erased later failed destination")
	}
	wrong := browserEvidenceCall(p, "visit", "snapshot", "https://example.com/a", true)
	browserEvidenceStep(p, "visit", "done", wrong)
	if p.completed() {
		t.Fatal("wrong destination accepted")
	}
	good := browserEvidenceCall(p, "visit", "open", "https://example.com/b", true)
	browserEvidenceStep(p, "visit", "done", good)
	if !p.completed() {
		t.Fatalf("verified recovery refused: %+v", p.snapshot())
	}
}
func TestBrowserFailureDoesNotPoisonOtherStepsOrChangeWithTitle(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "finished browser return", "done")
	browserEvidenceStep(p, "visit", "active")
	browserEvidenceCall(p, "visit", "open", "https://example.com/b", false)
	p.record([]messages.PlanStep{{ID: "visit", Title: "All sorted", Status: "done"}})
	browserEvidenceStep(p, "Verify function return values", "done")
	if got := p.unfinished(); len(got) != 1 || got[0] != "All sorted" {
		t.Fatalf("wrong obligations: %v", got)
	}
	if p.snapshot()[0].Status != "done" {
		t.Fatal("completed step was reopened")
	}
}
func TestBrowserEvidenceOwnershipAndCheckpoint(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	browserEvidenceStep(p, "b", "active")
	if _, err := p.beginBrowserCall("", map[string]any{"action": "snapshot"}); err == nil {
		t.Fatal("ambiguous activity assigned arbitrarily")
	}
	browserEvidenceCall(p, "a", "open", "https://example.com", false)
	other := browserEvidenceCall(p, "b", "open", "https://example.com", true)
	browserEvidenceStep(p, "a", "done", other)
	if p.snapshot()[0].Status == "done" {
		t.Fatal("cross-step evidence accepted")
	}
	restored := &planTracker{}
	restoreCheckpoint(checkpointFrom(withPlanTracker(context.Background(), p)), nil, restored, nil)
	good := browserEvidenceCall(restored, "a", "open", "https://example.com", true)
	browserEvidenceStep(restored, "a", "done", good)
	if restored.snapshot()[0].Status != "done" {
		t.Fatal("checkpointed failure could not recover")
	}
}
func TestLegacyBrowserFailureDoesNotReopenCompletedSteps(t *testing.T) {
	p := &planTracker{}
	restoreCheckpoint(&runCheckpoint{BrowserFailed: true, Plan: []messages.PlanStep{{ID: "a", Title: "browser", Status: "done"}, {ID: "b", Title: "work", Status: "active"}}}, nil, p, nil)
	if p.snapshot()[0].Status != "done" || p.snapshot()[1].Status != "blocked" {
		t.Fatal(p.snapshot())
	}
}

func TestBrowserPlanCannotFinishInFlightOrUseOlderConcurrentObservation(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	earlier := mustBeginBrowserCall(t, p, "a", map[string]any{"action": "open", "url": "https://example.com"})
	later := mustBeginBrowserCall(t, p, "a", map[string]any{"action": "open", "url": "https://example.com"})
	p.completeBrowserCall(later, `{"ok":false,"error":{"code":"timeout"}}`)
	browserEvidenceStep(p, "a", "done")
	if p.completed() {
		t.Fatal("finished while operation still running")
	}
	p.completeBrowserCall(earlier, `{"ok":true,"url":"https://example.com","snapshot":{"text":"old observation"}}`)
	browserEvidenceStep(p, "a", "done", earlier.receipt.ID)
	if p.completed() {
		t.Fatal("late delivery of an earlier observation erased later failure")
	}
}
func TestBrowserPendingCheckpointRequiresNewObservation(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	mustBeginBrowserCall(t, p, "a", map[string]any{"action": "open", "url": "https://example.com"})
	restored := &planTracker{}
	restoreCheckpoint(checkpointFrom(withPlanTracker(context.Background(), p)), nil, restored, nil)
	browserEvidenceStep(restored, "a", "done")
	if restored.completed() {
		t.Fatal("interrupted operation falsely completed")
	}
	id := browserEvidenceCall(restored, "a", "open", "https://example.com", true)
	browserEvidenceStep(restored, "a", "done", id)
	if !restored.completed() {
		t.Fatal("pending operation could not be recovered")
	}
}

func mustBeginBrowserCall(t *testing.T, p *planTracker, id string, args map[string]any) planBrowserCall {
	t.Helper()
	call, err := p.beginBrowserCall(id, args)
	if err != nil {
		t.Fatal(err)
	}
	return call
}
func callBrowserReceipt(t *testing.T, p *planTracker, id string, args map[string]any, result string) string {
	t.Helper()
	return p.completeBrowserCall(mustBeginBrowserCall(t, p, id, args), result)
}

func TestBrowserAdmissionBlocksDoneBeforeUnderlyingCallCompletes(t *testing.T) {
	p := &planTracker{}
	browserEvidenceStep(p, "a", "active")
	entered, release := make(chan struct{}), make(chan struct{})
	base := &evidenceBrowserTool{invoke: func(context.Context, map[string]any) (string, error) {
		close(entered)
		<-release
		return `{"ok":false,"error":{"code":"timeout"}}`, nil
	}}
	ctx := withPlanTracker(context.Background(), p)
	finished := make(chan error, 1)
	go func() {
		_, err := EinoTool(base).InvokableRun(ctx, `{"action":"open","url":"https://example.com"}`)
		finished <- err
	}()
	<-entered
	response, err := (planTool{}).Invoke(ctx, map[string]any{"steps": []any{map[string]any{"id": "a", "title": "a", "status": "done"}}})
	close(release)
	callErr := <-finished
	if err != nil || callErr != nil {
		t.Fatalf("plan=%v call=%v", err, callErr)
	}
	if p.completed() || !strings.Contains(response, "blocked") {
		t.Fatalf("in-flight plan completed: %s", response)
	}
	if len(p.checkpoint().Browser["id:a"].Failures) != 1 {
		t.Fatal("adapter lost failed receipt")
	}
}

func TestBrowserAdmissionKeepsNoPlanCallsAndRejectsCompletedSteps(t *testing.T) {
	for _, p := range []*planTracker{nil, {}, {steps: []messages.PlanStep{{ID: "a", Title: "a", Status: "done"}}}} {
		calls := 0
		base := &evidenceBrowserTool{invoke: func(context.Context, map[string]any) (string, error) { calls++; return `{"ok":true}`, nil }}
		out, err := EinoTool(base).InvokableRun(withPlanTracker(context.Background(), p), `{"action":"snapshot","plan_step_id":"a"}`)
		if err != nil {
			t.Fatal(err)
		}
		if p != nil && len(p.snapshot()) > 0 {
			if calls != 0 || !strings.Contains(out, "plan_step_id") {
				t.Fatalf("completed step dispatched: %s", out)
			}
		} else if calls != 1 {
			t.Fatal("no-plan browser call rejected")
		}
	}
}

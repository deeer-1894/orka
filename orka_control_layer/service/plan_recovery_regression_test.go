package service

import (
	"fmt"
	"sync"
	"testing"

	"github.com/orka-oss/orka_core/messages"
)

func setRecoveryStep(p *planTracker, status string, ids ...string) {
	p.record([]messages.PlanStep{{ID: "a", Title: "verify required interaction", Status: status, Reason: "verified required page", EvidenceIDs: ids}})
}
func recoveryCall(t *testing.T, p *planTracker, action, url string) planBrowserCall {
	t.Helper()
	c, err := p.beginBrowserCall("a", map[string]any{"action": action, "url": url})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func recoveryObservation(t *testing.T, p *planTracker, action, url string) planBrowserCall {
	t.Helper()
	c := recoveryCall(t, p, action, url)
	p.completeBrowserCall(c, fmt.Sprintf(`{"ok":true,"url":%q,"snapshot":{"text":"visible content"}}`, url))
	return c
}

func TestPlanRecoveryRegressionClickFailureClearedByUnrelatedPage(t *testing.T) {
	for _, identity := range []string{"receipt", "prior observation", "unknown"} {
		t.Run(identity, func(t *testing.T) {
			p := &planTracker{}
			setRecoveryStep(p, "active")
			if identity == "prior observation" {
				recoveryObservation(t, p, "open", "https://required.example/form")
			}
			call := recoveryCall(t, p, "click", "")
			url := ""
			if identity == "receipt" {
				url = "https://required.example/form"
			}
			p.completeBrowserCall(call, fmt.Sprintf(`{"ok":false,"url":%q,"error":{"code":"outcome_unknown"}}`, url))
			other := recoveryObservation(t, p, "open", "https://unrelated.example/news")
			setRecoveryStep(p, "done", other.receipt.ID)
			if p.completed() {
				t.Fatal("unrelated page observation closed uncertain submit")
			}
			good := recoveryObservation(t, p, "open", "https://required.example/form")
			setRecoveryStep(p, "done", good.receipt.ID)
			if got, want := p.completed(), identity != "unknown"; got != want {
				t.Fatalf("completed=%v want=%v for %s", got, want, identity)
			}
		})
	}
}

func TestPlanRecoveryRegressionAdmissionAndCompletionAreAtomic(t *testing.T) {
	for i := 0; i < 200; i++ {
		p := &planTracker{}
		setRecoveryStep(p, "active")
		start := make(chan struct{})
		var call planBrowserCall
		var err error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			call, err = p.beginBrowserCall("a", map[string]any{"action": "open", "url": "https://required.example"})
		}()
		go func() { defer wg.Done(); <-start; setRecoveryStep(p, "done") }()
		close(start)
		wg.Wait()
		if err != nil {
			if !p.completed() || len(p.checkpoint().Browser) != 0 {
				t.Fatal("rejected call changed completed plan")
			}
			continue
		}
		if p.completed() {
			t.Fatal("in-flight admission raced past done")
		}
		p.completeBrowserCall(call, `{"ok":false,"error":{"code":"timeout"}}`)
		if p.completed() {
			t.Fatal("failed dispatched work left plan completed")
		}
	}
}

func TestPlanRecoveryRegressionOverlappingObservationPrecedesFailureExecution(t *testing.T) {
	for _, finishFailureFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(finishFailureFirst), func(t *testing.T) {
			p := &planTracker{}
			setRecoveryStep(p, "active")
			failed := recoveryCall(t, p, "open", "https://required.example")
			overlap := recoveryCall(t, p, "snapshot", "")
			fail := func() { p.completeBrowserCall(failed, `{"ok":false,"error":{"code":"timeout"}}`) }
			observe := func() {
				p.completeBrowserCall(overlap, `{"ok":true,"url":"https://required.example","snapshot":{"text":"old page"}}`)
			}
			if finishFailureFirst {
				fail()
				observe()
			} else {
				observe()
				fail()
			}
			setRecoveryStep(p, "done", overlap.receipt.ID)
			if p.completed() {
				t.Fatal("overlapping observation accepted as recovery")
			}
			fresh := recoveryObservation(t, p, "snapshot", "https://required.example")
			setRecoveryStep(p, "done", fresh.receipt.ID)
			if !p.completed() {
				t.Fatal("causally later observation could not recover")
			}
		})
	}
}

func TestPlanRecoveryRegressionLateDuplicateFailureAdvancesFrontier(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	first := recoveryCall(t, p, "open", "https://required.example")
	second := recoveryCall(t, p, "open", "https://required.example")
	p.completeBrowserCall(second, `{"ok":false,"error":{"code":"timeout"}}`)
	observed := recoveryObservation(t, p, "snapshot", "https://required.example")
	p.completeBrowserCall(first, `{"ok":false,"error":{"code":"timeout"}}`)
	setRecoveryStep(p, "done", observed.receipt.ID)
	if p.completed() {
		t.Fatal("late older failure lost its causal boundary")
	}
	fresh := recoveryObservation(t, p, "snapshot", "https://required.example")
	setRecoveryStep(p, "done", fresh.receipt.ID)
	if !p.completed() {
		t.Fatal("late failure could not recover")
	}
}

func TestPlanRecoveryRegressionMoreFailuresThanReceiptWindow(t *testing.T) {
	for _, status := range []string{"active", "blocked", "done"} {
		t.Run(status, func(t *testing.T) {
			p := &planTracker{}
			setRecoveryStep(p, "active")
			for i := 0; i < 40; i++ {
				c := recoveryCall(t, p, "open", fmt.Sprintf("https://example.com/%d", i))
				p.completeBrowserCall(c, `{"ok":false,"error":{"code":"timeout"}}`)
			}
			for i := 0; i < 40; i++ {
				c := recoveryObservation(t, p, "open", fmt.Sprintf("https://example.com/%d", i))
				setRecoveryStep(p, status, c.receipt.ID)
				if got := len(p.checkpoint().Browser["id:a"].Failures); got != 39-i {
					t.Fatalf("failures=%d after verifying %d", got, i)
				}
				if i == 19 {
					restored := &planTracker{}
					restored.restore(p.checkpoint(), false)
					p = restored
				}
			}
			setRecoveryStep(p, "done")
			if !p.completed() {
				t.Fatal("fully verified destinations remained stuck behind the receipt window")
			}
		})
	}
}

func TestPlanRecoveryRegressionEvictedEvidenceCanBeReobserved(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	for i := 0; i < 33; i++ {
		c := recoveryCall(t, p, "open", fmt.Sprintf("https://example.com/%d", i))
		p.completeBrowserCall(c, `{"ok":false,"error":{"code":"timeout"}}`)
	}
	ids := []string{}
	for i := 0; i < 33; i++ {
		c := recoveryObservation(t, p, "open", fmt.Sprintf("https://example.com/%d", i))
		ids = append(ids, c.receipt.ID)
	}
	setRecoveryStep(p, "done", ids[1:]...)
	if p.completed() || len(p.checkpoint().Browser["id:a"].Failures) != 1 {
		t.Fatal("partial verification did not clear exactly 32 failures")
	}
	c := recoveryObservation(t, p, "open", "https://example.com/0")
	setRecoveryStep(p, "done", c.receipt.ID)
	if !p.completed() {
		t.Fatal("eviction made a verified destination permanently unresolvable")
	}
}

func TestPlanRecoveryRegressionCheckpointRepairsMixedLegacyState(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	failed := recoveryCall(t, p, "open", "https://required.example")
	p.completeBrowserCall(failed, `{"ok":false,"error":{"code":"timeout"}}`)
	saved := p.checkpoint()
	saved.Steps[0].Status = "done" // An old non-atomic checkpoint can contain this mismatch.
	saved.Steps = append(saved.Steps, messages.PlanStep{ID: "b", Title: "finished independent work", Status: "done"})
	p.restore(saved, false)
	if p.completed() || p.snapshot()[0].Status != "blocked" || p.snapshot()[1].Status != "done" {
		t.Fatal("restore closed failed work or reopened unrelated completion")
	}
	good := recoveryObservation(t, p, "open", "https://required.example")
	setRecoveryStep(p, "done", good.receipt.ID)
	if !p.completed() {
		t.Fatal("repaired checkpoint could not recover")
	}
	saved = p.checkpoint()
	for i := 0; i < 2; i++ {
		p.restore(saved, false)
		if !p.completed() {
			t.Fatal("restoration reopened verified work")
		}
	}
	// Returned snapshots cannot alias the live tracker.
	saved.Steps[0].EvidenceIDs[0] = "mutated"
	saved.Browser["id:a"].Receipts[0].URL = "mutated"
	if p.snapshot()[0].EvidenceIDs[0] == "mutated" || p.checkpoint().Browser["id:a"].Receipts[0].URL == "mutated" {
		t.Fatal("checkpoint aliases tracker")
	}
}

func TestPlanRecoveryRegressionConcurrentCheckpointAndRestore(t *testing.T) {
	good := planCheckpoint{Steps: []messages.PlanStep{{ID: "a", Status: "done", Reason: "good"}}}
	bad := planCheckpoint{Steps: []messages.PlanStep{{ID: "a", Status: "active", Reason: "bad"}}, Browser: map[string]planBrowserEvidence{"id:a": {Sequence: 1, Generation: 1, Failures: map[string]planBrowserReceipt{"failure": {ID: "failed", Sequence: 1, Completed: 1, Action: "open", TargetURL: "https://example.com", Code: "timeout"}}}}}
	p := &planTracker{}
	p.restore(good, false)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			p.restore(bad, false)
			p.restore(good, false)
		}
	}()
	for i := 0; i < 1000; i++ {
		saved := p.checkpoint()
		failures := len(saved.Browser["id:a"].Failures)
		if (saved.Steps[0].Status == "done") != (failures == 0) {
			t.Error("checkpoint mixed plan and browser generations")
			break
		}
	}
	wg.Wait()
}

func TestPlanRecoveryRegressionRestoreRejectsOldSequenceAsCausality(t *testing.T) {
	p := &planTracker{}
	p.restore(planCheckpoint{
		Steps:   []messages.PlanStep{{ID: "a", Title: "original", Status: "blocked"}},
		Browser: map[string]planBrowserEvidence{"id:a": {Sequence: 2, Receipts: []planBrowserReceipt{{ID: "old-success", Sequence: 2, Action: "open", URL: "https://example.com", TargetURL: "https://example.com", OK: true, Observed: true}}, Failures: map[string]planBrowserReceipt{"old-failure": {ID: "old-failure", Sequence: 1, Action: "open", TargetURL: "https://example.com", Code: "timeout"}}}},
	}, false)
	setRecoveryStep(p, "done", "old-success")
	if p.completed() {
		t.Fatal("legacy sequence was treated as causal recovery")
	}
	good := recoveryObservation(t, p, "open", "https://example.com")
	setRecoveryStep(p, "done", good.receipt.ID)
	if !p.completed() {
		t.Fatal("legacy evidence could not be reverified")
	}
}

func TestPlanRecoveryRegressionRestoredCallIgnoresStaleCompletion(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	old := recoveryCall(t, p, "open", "https://example.com")
	p.restore(p.checkpoint(), false)
	next := recoveryCall(t, p, "open", "https://example.com")
	if out := p.completeBrowserCall(old, `{"ok":true,"url":"https://example.com","snapshot":{"text":"stale"}}`); out != "" {
		t.Fatal("stale callback was recorded")
	}
	if out := p.completeBrowserCall(next, `{"ok":true,"url":"https://example.com","snapshot":{"text":"fresh"}}`); out == "" {
		t.Fatal("current callback was lost")
	}
	setRecoveryStep(p, "done", next.receipt.ID)
	if !p.completed() {
		t.Fatal("pending checkpoint could not recover")
	}
}

func TestPlanRecoveryRegressionEvidenceRequiresReasonAndStepIdentity(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	failed := recoveryCall(t, p, "open", "https://example.com")
	p.completeBrowserCall(failed, `{"ok":false,"error":{"code":"timeout"}}`)
	good := recoveryObservation(t, p, "open", "https://example.com")
	p.record([]messages.PlanStep{{ID: "a", Title: "a", Status: "done", EvidenceIDs: []string{good.receipt.ID}}})
	if p.completed() || len(p.checkpoint().Browser["id:a"].Failures) != 1 {
		t.Fatal("evidence without explanation cleared a failure")
	}
	setRecoveryStep(p, "done", "another-step-receipt")
	if p.completed() {
		t.Fatal("unknown evidence cleared a failure")
	}
	setRecoveryStep(p, "done", good.receipt.ID)
	if !p.completed() {
		t.Fatal("valid explicit verification was rejected")
	}
}

func TestPlanRecoveryPreDispatchFailureWithUnknownPage(t *testing.T) {
	for _, code := range []string{"stale_ref", "invalid_arguments", "unsupported_action", "invalid_request", "not_ready", "ambiguous_selector"} {
		t.Run(code, func(t *testing.T) {
			p := &planTracker{}
			setRecoveryStep(p, "active")
			c := recoveryCall(t, p, "click", "")
			p.completeBrowserCall(c, fmt.Sprintf(`{"ok":false,"error":{"code":%q}}`, code))
			p.restore(p.checkpoint(), false)
			observed := recoveryObservation(t, p, "snapshot", "https://required.example/form")
			clicked := recoveryObservation(t, p, "click", "https://required.example/success")
			setRecoveryStep(p, "done", observed.receipt.ID, clicked.receipt.ID)
			if !p.completed() {
				t.Fatal("pre-dispatch rejection created a permanent unknown-page obligation", p.snapshot())
			}
		})
	}
}

func TestPlanRecoverySuccessfulEvaluationKeepsPageAssociation(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	recoveryObservation(t, p, "snapshot", "https://required.example/form")
	c := recoveryCall(t, p, "evaluate", "")
	p.completeBrowserCall(c, `{"ok":true,"action":"evaluate","value":42}`)
	c = recoveryCall(t, p, "click", "")
	p.completeBrowserCall(c, `{"ok":false,"error":{"code":"outcome_unknown"}}`)
	other := recoveryObservation(t, p, "open", "https://unrelated.example/")
	setRecoveryStep(p, "done", other.receipt.ID)
	if p.completed() {
		t.Fatal("unrelated page cleared uncertain click")
	}
	good := recoveryObservation(t, p, "open", "https://required.example/form")
	setRecoveryStep(p, "done", good.receipt.ID)
	if !p.completed() {
		t.Fatal("successful evaluation without snapshot erased associated page")
	}
}

func TestPlanRecoveryPartialFormIsNotPreDispatch(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	c := recoveryCall(t, p, "fill_form", "")
	p.completeBrowserCall(c, `{"ok":false,"form":{"completed":1,"total":2,"failed_index":1},"error":{"code":"stale_ref"}}`)
	other := recoveryObservation(t, p, "open", "https://unrelated.example/")
	setRecoveryStep(p, "done", other.receipt.ID)
	if p.completed() {
		t.Fatal("partial form was incorrectly treated as wholly undispatched")
	}
}

func TestPlanRecoveryClickNavigationUsesDirectPageObservation(t *testing.T) {
	for _, scenario := range []string{"direct", "restore", "retry observation", "other page", "other epoch", "intervening open", "other step", "overlapping"} {
		t.Run(scenario, func(t *testing.T) {
			p := &planTracker{}
			setRecoveryStep(p, "active")
			recoveryObservation(t, p, "snapshot", "https://required.example/form")
			c := recoveryCall(t, p, "click", "")
			var overlap planBrowserCall
			if scenario == "overlapping" {
				overlap = recoveryCall(t, p, "open", "https://unrelated.example/")
			}
			p.completeBrowserCall(c, `{"ok":false,"page_id":"page","page_epoch":7,"error":{"code":"outcome_unknown"}}`)
			switch scenario {
			case "restore":
				p.restore(p.checkpoint(), false)
			case "retry observation":
				read := recoveryCall(t, p, "snapshot", "")
				p.completeBrowserCall(read, `{"ok":false,"page_id":"page","page_epoch":7,"error":{"code":"observation_failed"}}`)
			case "intervening open":
				recoveryObservation(t, p, "open", "https://required.example/success")
			case "other step":
				browserEvidenceStep(p, "b", "active")
				other := mustBeginBrowserCall(t, p, "b", map[string]any{"action": "open", "url": "https://required.example/success"})
				p.completeBrowserCall(other, `{"ok":true,"url":"https://required.example/success","snapshot":{"text":"other work"}}`)
			case "overlapping":
				p.completeBrowserCall(overlap, `{"ok":true,"url":"https://required.example/success","snapshot":{"text":"other work"}}`)
			}
			pageID, epoch := "page", 7
			if scenario == "other page" {
				pageID = "different"
			}
			if scenario == "other epoch" {
				epoch++
			}
			observed := recoveryCall(t, p, "snapshot", "")
			p.completeBrowserCall(observed, fmt.Sprintf(`{"ok":true,"page_id":%q,"page_epoch":%d,"url":"https://required.example/success","snapshot":{"text":"submission accepted"}}`, pageID, epoch))
			setRecoveryStep(p, "done", observed.receipt.ID)
			want := scenario == "direct" || scenario == "restore" || scenario == "retry observation"
			if got := p.snapshot()[0].Status == "done"; got != want {
				t.Fatalf("navigation verified=%v want=%v: %+v", got, want, p.snapshot())
			}
		})
	}
}

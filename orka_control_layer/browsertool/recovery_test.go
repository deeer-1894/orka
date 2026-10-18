package browsertool

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

func TestFixedBrowserHelperBundleMatchesCanonicalSources(t *testing.T) {
	raw, err := os.ReadFile("../../gui_agent/service/browser_helpers.json")
	if err != nil {
		t.Fatal(err)
	}
	var bundled map[string]string
	if err := json.Unmarshal(raw, &bundled); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"dom": domScript, "observation": observationScript, "snapshot": snapshotScript, "settle": settleScript} {
		if bundled[name] != source {
			t.Errorf("%s bundle is stale; run python3 gui_agent/scripts/sync_browser_helpers.py", name)
		}
	}
}

func TestObservationRetriesContextLossWithoutRepeatingInput(t *testing.T) {
	lease := &fixtureLease{}
	attempts, inputs := 0, 0
	lease.handler = func(method string, params, out any) (bool, error) {
		if method == "Input.dispatchMouseEvent" {
			inputs++
		}
		if method == "Orka.observe" {
			if params.(map[string]any)["operation"] == "snapshot" {
				attempts++
				if attempts == 1 {
					return true, &connectors.BrowserError{Code: "context_lost"}
				}
			}
		}
		return false, nil
	}
	r, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "click", Selector: "button"}, nil)
	if err != nil || !r.OK || r.Snapshot == nil || attempts != 2 || inputs != 2 {
		t.Fatalf("observation recovery replayed input or lost receipt: attempts=%d inputs=%d result=%+v err=%v", attempts, inputs, r, err)
	}
}

func TestSnapshotDiscardsRaceAndReturnsFullReplacement(t *testing.T) {
	lease := &fixtureLease{}
	states, snapshots := 0, 0
	lease.handler = func(method string, params, out any) (bool, error) {
		if method == "Orka.getPageState" {
			states++
			revision := 1
			if states > 2 {
				revision = 2
			}
			raw, _ := json.Marshal(map[string]any{"loading": false, "revision": revision})
			return true, json.Unmarshal(raw, out)
		}
		if method == "Orka.observe" {
			if params.(map[string]any)["operation"] == "snapshot" {
				snapshots++
				request := params.(map[string]any)["request"].(map[string]any)
				if snapshots > 1 && request["view"] != "full" {
					t.Error("discarded observation's delta baseline escaped")
				}
			}
		}
		return false, nil
	}
	r, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "snapshot"}, nil)
	if err != nil || !r.OK || snapshots != 2 {
		t.Fatalf("racy snapshot was not replaced: %d %+v %v", snapshots, r, err)
	}
}

func TestLoadingDocumentCannotReturnOldReadyPage(t *testing.T) {
	lease := &fixtureLease{handler: func(method string, params, out any) (bool, error) {
		if method == "Orka.getPageState" {
			return true, json.Unmarshal([]byte(`{"loading":true,"revision":1}`), out)
		}
		return false, nil
	}}
	r, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "snapshot", TimeoutMS: 50}, nil)
	if err == nil || r.OK || r.Snapshot != nil {
		t.Fatalf("old document exposed while navigation was pending: %+v %v", r, err)
	}
}

type closingEpochLease struct{ fixtureLease }

type stalledObservationLease struct {
	fixtureLease
	inputs int
}

func (s *stalledObservationLease) Execute(ctx context.Context, method string, params, out any) error {
	if method == "Input.dispatchMouseEvent" {
		s.inputs++
	}
	if method == "Orka.observe" && params.(map[string]any)["operation"] == "settle" {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.fixtureLease.Execute(ctx, method, params, out)
}
func TestObservationDeadlineDoesNotInheritActionTimeout(t *testing.T) {
	lease := &stalledObservationLease{}
	started := time.Now()
	r, err := NewEngine(recoveryDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "click", Selector: "button", TimeoutMS: 15000}, nil)
	if err == nil || r.Error.Code != "observation_failed" || lease.inputs != 2 {
		t.Fatalf("lost outcome or replayed input: %+v inputs=%d", r, lease.inputs)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("observation inherited action timeout: %v", elapsed)
	}
}

func (l *closingEpochLease) Info() connectors.BrowserPageInfo {
	info := l.fixtureLease.Info()
	info.PageEpoch += int64(l.closed)
	return info
}

type recoveryDialer struct{ lease connectors.BrowserLease }

func (d recoveryDialer) Acquire(context.Context, connectors.GUIIdentity, time.Duration, time.Duration) (connectors.BrowserLease, error) {
	return d.lease, nil
}

func TestNavigationAtReleaseCannotLabelOldRefsWithNewEpoch(t *testing.T) {
	lease := &closingEpochLease{}
	r, err := NewEngine(recoveryDialer{lease}).Run(context.Background(), testIdentity(), Request{Action: "snapshot"}, nil)
	if err == nil || r.Error.Code != "observation_failed" || r.Snapshot != nil || r.PageEpoch != 8 {
		t.Fatalf("obsolete refs escaped with release epoch: %+v %v", r, err)
	}
}

func TestContextLossDuringUserMutationRemainsUnknown(t *testing.T) {
	for _, req := range []Request{{Action: "fill", Selector: "input", Text: "once"}, {Action: "evaluate", Expression: "location.href='/next'"}} {
		t.Run(req.Action, func(t *testing.T) {
			calls := 0
			lease := &fixtureLease{handler: func(method string, params, out any) (bool, error) {
				if method == "Runtime.callFunctionOn" || method == "Orka.act" {
					calls++
					return true, &connectors.BrowserError{Code: "context_lost"}
				}
				return false, nil
			}}
			r, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), req, nil)
			if err == nil || r.Error.Code != "outcome_unknown" || calls != 1 {
				t.Fatalf("mutation's context loss replayed or hidden: %+v %v calls=%d", r, err, calls)
			}
		})
	}
}

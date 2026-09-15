package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/acceptance"
	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/config"
	"github.com/orka-oss/orka_core/pathsafe"
)

func TestAcceptanceAuditRetainsOriginalRequestsAndRemovedCriteria(t *testing.T) {
	base := t.TempDir()
	root, err := pathsafe.EnsureSession(base, "owner", "one")
	if err != nil {
		t.Fatal(err)
	}
	ctx := withDelivery(withAcceptance(context.Background(), base, "owner", "one", "run-one"), newDeliveryTracker(root))
	if err := persistAcceptanceContract(ctx, []string{"original requirement", "changed constraint"}); err != nil {
		t.Fatal(err)
	}
	if err := persistAcceptanceContract(ctx, []string{"forged replacement"}); err == nil {
		t.Fatal("original request replaced")
	}
	if err := os.WriteFile(filepath.Join(root, "result.csv"), []byte("region,value\nSouth,35\nNorth,20\nALL,55\n"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := acceptance.Spec{Kind: acceptance.Kind, Requirements: []acceptance.Requirement{
		{ID: "range", Method: "csv", File: "result.csv", Column: "value", Operation: "max", Exclude: map[string]string{"region": "ALL"}, Expected: "35"},
		{ID: "source", Method: "manual", Description: "independent source review"},
	}}
	write := func() {
		t.Helper()
		b, _ := json.Marshal(spec)
		if err := os.WriteFile(filepath.Join(root, "result.acceptance.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	out, err := (acceptanceCheckTool{}).Invoke(ctx, map[string]any{"path": "result.acceptance.json"})
	if err != nil {
		t.Fatal(err)
	}
	var report acceptance.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Results[1].Status != "unverified" {
		t.Fatal("manual assertion presented as verified")
	}
	spec.Requirements = spec.Requirements[:1]
	write()
	if len(acceptanceFailures(ctx)) == 0 {
		t.Fatal("deleting criterion made task pass")
	}
	svc := &ChatService{Cfg: &config.Config{Storage: config.StorageConfig{BaseStoragePath: base}}}
	history, err := svc.AcceptanceFor("owner", "one", "run-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Contract.Requests) != 2 || history.Contract.Requests[0] != "original requirement" || len(history.Checks) != 2 {
		t.Fatalf("history=%+v", history)
	}
	foreign, err := svc.AcceptanceFor("other", "one", "run-one")
	if err != nil || len(foreign.Checks) > 0 || len(foreign.Contract.Requests) > 0 {
		t.Fatal("cross owner audit exposed")
	}
	if _, err := os.Stat(filepath.Join(root, ".orka_acceptance")); !os.IsNotExist(err) {
		t.Fatal("audit stored in writable workspace")
	}
}

// A resumed attempt must preserve checked obligations even after serialization
// and another process, without importing unrelated tasks in the same workspace.
func TestAcceptanceResumeRetainsManualObligation(t *testing.T) {
	base := t.TempDir()
	root, err := pathsafe.EnsureSession(base, "owner", "one")
	if err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "result.acceptance.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "result.txt"), []byte("verified"), 0600); err != nil {
		t.Fatal(err)
	}
	old := withDelivery(withAcceptance(context.Background(), base, "owner", "one", "run-first"), newDeliveryTracker(root))
	if err := persistAcceptanceContract(old, []string{"independent source review required"}); err != nil {
		t.Fatal(err)
	}
	write(`{"kind":"orka.acceptance/v1","requirements":[{"id":"content","method":"contains","file":"result.txt","expected":"verified"},{"id":"source","method":"manual"}]}`)
	if _, err := (acceptanceCheckTool{}).Invoke(old, map[string]any{"path": "result.acceptance.json"}); err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(checkpointFrom(old))
	var checkpoint runCheckpoint
	if err := json.Unmarshal(saved, &checkpoint); err != nil {
		t.Fatal(err)
	}
	resumed := withAcceptance(withRunResume(context.Background(), &runResume{Checkpoint: &checkpoint}), base, "owner", "one", "run-next")
	resumed = withDelivery(resumed, newDeliveryTracker(root))
	restoreCheckpoint(&checkpoint, nil, &planTracker{}, deliveryFrom(resumed))
	if err := persistAcceptanceContract(resumed, []string{"continue"}); err != nil {
		t.Fatal(err)
	}
	write(`{"kind":"orka.acceptance/v1","requirements":[{"id":"content","method":"contains","file":"result.txt","expected":"verified"}]}`)
	out := assessRunOutcome(&agent.RunContext{Ctx: resumed}, nil, nil)
	if out.status != db.RunPartial {
		t.Fatalf("removed inherited manual obligation became %s", out.status)
	}
	svc := &ChatService{Cfg: &config.Config{Storage: config.StorageConfig{BaseStoragePath: base}}}
	history, err := svc.AcceptanceFor("owner", "one", "run-next")
	if err != nil || len(history.Checks) != 2 {
		t.Fatalf("inherited history=%+v error=%v", history, err)
	}
	if len(history.Contract.InheritedRunIDs) != 1 || history.Contract.InheritedRunIDs[0] != "run-first" || history.Checks[0].RunID != "run-first" {
		t.Fatalf("lineage missing from API: %+v", history)
	}
	// A second recovery must retain the entire chain, including after a journal
	// round trip, rather than replacing the grandparent with its immediate parent.
	saved, _ = json.Marshal(checkpointFrom(resumed))
	if err := json.Unmarshal(saved, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(checkpoint.AcceptanceRunIDs, []string{"run-next", "run-first"}) {
		t.Fatalf("checkpoint lost lineage: %v", checkpoint.AcceptanceRunIDs)
	}
	third := withDelivery(withAcceptance(withRunResume(context.Background(), &runResume{Checkpoint: &checkpoint}), base, "owner", "one", "run-third"), newDeliveryTracker(root))
	restoreCheckpoint(&checkpoint, nil, &planTracker{}, deliveryFrom(third))
	if err := persistAcceptanceContract(third, []string{"continue again"}); err != nil {
		t.Fatal(err)
	}
	if got := assessRunOutcome(&agent.RunContext{Ctx: third}, nil, nil); got.status != db.RunPartial {
		t.Fatalf("grandparent obligation lost: %+v", got)
	}
	history, err = svc.AcceptanceFor("owner", "one", "run-third")
	if err != nil || len(history.Checks) != 3 || !reflect.DeepEqual(history.Contract.InheritedRunIDs, []string{"run-next", "run-first"}) {
		t.Fatalf("third history=%+v error=%v", history, err)
	}
	if err := deliveryFrom(third).declare([]string{"result.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.publishDelivery(third); err == nil || !strings.Contains(err.Error(), "acceptance") {
		t.Fatalf("snapshot omitted inherited manual requirement: %v", err)
	}
	foreign, err := svc.AcceptanceFor("other", "one", "run-third")
	if err != nil || len(foreign.Checks) > 0 || len(foreign.Contract.InheritedRunIDs) > 0 {
		t.Fatalf("cross-owner lineage visible: %+v %v", foreign, err)
	}
	// A genuinely new task in the same conversation is not a continuation.
	fresh := withDelivery(withAcceptance(context.Background(), base, "owner", "one", "run-unrelated"), newDeliveryTracker(root))
	if err := deliveryFrom(fresh).declare([]string{"result.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (acceptanceCheckTool{}).Invoke(fresh, map[string]any{"path": "result.acceptance.json"}); err != nil {
		t.Fatal(err)
	}
	if got := assessRunOutcome(&agent.RunContext{Ctx: fresh}, nil, nil); got.status != db.RunDone {
		t.Fatalf("independent task inherited obligations: %+v", got)
	}
}

func TestAcceptanceChainRejectsInvalidHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"cycle", []string{"run-next"}},
		{"duplicate", []string{"run-first", "run-first"}},
		{"escape", []string{"../other"}},
		{"too long", func() []string {
			ids := []string{}
			for i := 0; i < 33; i++ {
				ids = append(ids, fmt.Sprintf("run-%d", i))
			}
			return ids
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"acceptance_run_ids": tc.ids})
			var cp runCheckpoint
			if err := json.Unmarshal(body, &cp); err != nil {
				t.Fatal(err)
			}
			ctx := withAcceptance(withRunResume(context.Background(), &runResume{Checkpoint: &cp}), t.TempDir(), "owner", "one", "run-next")
			if _, err := readAcceptance(ctx.Value(acceptanceScopeKey{}).(acceptanceScope)); err == nil {
				t.Fatal("invalid history chain accepted")
			}
		})
	}
}

func TestAcceptanceHistoryRejectsPersistedCycle(t *testing.T) {
	base := t.TempDir()
	for _, pair := range [][2]string{{"run-a", "run-b"}, {"run-b", "run-a"}} {
		ctx := withAcceptance(context.Background(), base, "owner", "one", pair[0])
		dir, err := acceptanceDir(ctx.Value(acceptanceScopeKey{}).(acceptanceScope))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]any{"run_id": pair[0], "requests": []string{}, "inherited_run_ids": []string{pair[1]}})
		if err := os.WriteFile(filepath.Join(dir, "contract.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := &ChatService{Cfg: &config.Config{Storage: config.StorageConfig{BaseStoragePath: base}}}
	if _, err := svc.AcceptanceFor("owner", "one", "run-a"); err == nil {
		t.Fatal("persisted cycle accepted")
	}
}

func TestAcceptanceLegacyWithoutHistoryRemainsCompatible(t *testing.T) {
	base := t.TempDir()
	ctx := withAcceptance(withRunResume(context.Background(), &runResume{Checkpoint: &runCheckpoint{}}), base, "owner", "one", "legacy-resumed")
	if err := persistAcceptanceContract(ctx, []string{"continue legacy"}); err != nil {
		t.Fatal(err)
	}
	history, err := readAcceptance(ctx.Value(acceptanceScopeKey{}).(acceptanceScope))
	if err != nil || len(history.Checks) != 0 || len(history.Contract.InheritedRunIDs) != 0 {
		t.Fatalf("legacy history=%+v err=%v", history, err)
	}
	if got := assessRunOutcome(&agent.RunContext{Ctx: ctx}, nil, nil); got.status != db.RunDone {
		t.Fatalf("legacy no-history blocked: %+v", got)
	}
}

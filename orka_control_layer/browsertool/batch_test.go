package browsertool

import (
	"encoding/json"
	"testing"

	"github.com/orka-oss/orka_control_layer/connectors"
)

func invokeBrowser(t *testing.T, dialer connectors.BrowserDialer, args map[string]any) Result {
	t.Helper()
	out, err := New(dialer, t.TempDir()).Invoke(browserTestContext(), args)
	var result Result
	if err != nil || json.Unmarshal([]byte(out), &result) != nil {
		t.Fatalf("browser result: %s %v", out, err)
	}
	return result
}

func TestBatchValidatesEveryStepBeforeTouchingThePage(t *testing.T) {
	click := map[string]any{"action": "click", "selector": "button"}
	cases := []map[string]any{
		{"action": "batch"},
		{"action": "batch", "steps": []any{}},
		{"action": "batch", "steps": []any{click, click, click, click, click, click, click, click, click}},
		{"action": "batch", "steps": []any{click, map[string]any{"action": "open", "url": "https://example.test"}}},
		{"action": "batch", "steps": []any{click, map[string]any{"action": "batch", "steps": []any{click}}}},
		{"action": "batch", "steps": []any{click, map[string]any{"action": "fill", "selector": "input"}}},
		{"action": "batch", "steps": []any{click}, "url": "https://example.test"},
	}
	for _, args := range cases {
		dialer := &countingDialer{}
		result := invokeBrowser(t, dialer, args)
		if result.OK || result.Error == nil || result.Error.Code != "invalid_arguments" || result.Action != batchAction || dialer.calls != 0 {
			t.Fatalf("invalid batch reached the page: args=%v result=%+v acquires=%d", args, result, dialer.calls)
		}
	}
}

func TestBatchRunsStepsInOrderAndReturnsFinalObservation(t *testing.T) {
	dialer := &fixtureDialer{lease: &fixtureLease{}}
	result := invokeBrowser(t, dialer, map[string]any{"action": "batch", "steps": []any{
		map[string]any{"action": "fill", "selector": "input", "text": "user"},
		map[string]any{"action": "click", "selector": "button"},
		map[string]any{"action": "snapshot"},
	}})
	if !result.OK || result.Action != batchAction || result.Snapshot == nil || result.URL != "https://fixture.test/" {
		t.Fatalf("batch result = %+v", result)
	}
	if len(result.Steps) != 3 || result.Steps[0].Action != "fill" || result.Steps[1].Action != "click" || result.Steps[2].Action != "snapshot" || dialer.acquires != 3 {
		t.Fatalf("steps=%+v acquires=%d", result.Steps, dialer.acquires)
	}
}

func TestBatchStopsAtFirstFailureWithoutRunningLaterSteps(t *testing.T) {
	acts := 0
	lease := &fixtureLease{handler: func(method string, _, _ any) (bool, error) {
		if method != "Orka.act" {
			return false, nil
		}
		acts++
		if acts == 2 {
			return true, NewActionError("target_not_found", "fixture target missing")
		}
		return false, nil
	}}
	dialer := &fixtureDialer{lease: lease}
	result := invokeBrowser(t, dialer, map[string]any{"action": "batch", "steps": []any{
		map[string]any{"action": "fill", "selector": "input", "text": "user"},
		map[string]any{"action": "click", "selector": "button"},
		map[string]any{"action": "click", "selector": "#never"},
	}})
	if result.OK || result.Error == nil || len(result.Steps) != 2 || !result.Steps[0].OK || result.Steps[1].OK {
		t.Fatalf("batch did not stop at the failing step: %+v", result)
	}
	if acts != 2 || dialer.acquires != 2 {
		t.Fatalf("later step ran or a step was replayed: acts=%d acquires=%d", acts, dialer.acquires)
	}
}

func TestBatchSharedSnapshotIDAppliesToRefSteps(t *testing.T) {
	steps, err := parseBatch(map[string]any{"action": "batch", "snapshot_id": "snap-1", "steps": []any{
		map[string]any{"action": "fill", "ref": "e1", "text": "user"},
		map[string]any{"action": "click", "ref": "e2", "snapshot_id": "snap-2"},
		map[string]any{"action": "click", "selector": "#go"},
	}})
	if err != nil || steps[0].SnapshotID != "snap-1" || steps[1].SnapshotID != "snap-2" || steps[2].SnapshotID != "" {
		t.Fatalf("steps=%+v err=%v", steps, err)
	}
	if _, err := parseBatch(map[string]any{"action": "batch", "snapshot_id": "", "steps": []any{map[string]any{"action": "click", "ref": "e1"}}}); err == nil {
		t.Fatal("empty shared snapshot_id accepted")
	}
}

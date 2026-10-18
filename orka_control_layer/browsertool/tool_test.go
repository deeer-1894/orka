package browsertool

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

type countingDialer struct{ calls int }

func (d *countingDialer) Acquire(context.Context, connectors.GUIIdentity, time.Duration, time.Duration) (connectors.BrowserLease, error) {
	d.calls++
	return nil, NewActionError("unavailable", "fixture has no page")
}

type sequenceDialer struct {
	leases   []connectors.BrowserLease
	acquires int
}

func (d *sequenceDialer) Acquire(context.Context, connectors.GUIIdentity, time.Duration, time.Duration) (connectors.BrowserLease, error) {
	if d.acquires >= len(d.leases) {
		return nil, NewActionError("unavailable", "no fixture lease")
	}
	lease := d.leases[d.acquires]
	d.acquires++
	return lease, nil
}
func browserTestContext() context.Context {
	return connectors.WithGUIIdentity(context.Background(), connectors.GUIIdentity{OwnerID: "owner", ConversationID: "conversation", RunID: "run"})
}

func TestToolRejectsInvalidArgumentsBeforeAcquiringPage(t *testing.T) {
	cases := []map[string]any{
		{"action": "open", "url": "file:///etc/passwd"}, {"action": "open", "url": "javascript:alert(1)"},
		{"action": "open", "url": "https://example.test", "expression": "secret"},
		{"action": "fill", "selector": "input"}, {"action": "fill", "selector": "input", "text": "x", "ref": "r", "snapshot_id": "s"},
		{"action": "click", "ref": "r"}, {"action": "click", "snapshot_id": "s"},
		{"action": "snapshot", "text": "secret"}, {"action": "snapshot", "owner_id": "forged"},
		{"action": "evaluate", "expression": strings.Repeat("x", MaxExpressionBytes+1)},
		{"action": "scroll", "amount": math.Inf(1)}, {"action": "scroll", "amount": 0},
		{"action": "wait", "condition": "text", "expression": "x"}, {"action": "wait", "condition": "visible"},
		{"action": "screenshot", "path": "../secret.png"}, {"action": "screenshot", "path": ".."},
		{"action": "download", "path": "x", "url": "data:text/plain,x"},
		{"action": "snapshot", "timeout_ms": 60001}, {"action": "snapshot", "timeout_ms": 1.5},
	}
	for _, args := range cases {
		dialer := &countingDialer{}
		tool := New(dialer, t.TempDir())
		out, err := tool.Invoke(browserTestContext(), args)
		if err != nil {
			t.Fatal(err)
		}
		var result Result
		if json.Unmarshal([]byte(out), &result) != nil || result.OK || result.Error == nil || result.Error.Code != "invalid_arguments" || dialer.calls != 0 {
			t.Fatalf("invalid call reached page: args=%v result=%s acquires=%d", args, out, dialer.calls)
		}
	}
}
func TestToolAllowsEmptyInputAndRequiresTrustedIdentity(t *testing.T) {
	for _, args := range []map[string]any{{"action": "fill", "selector": "input", "text": ""}, {"action": "select", "selector": "select", "value": ""}} {
		dialer := &countingDialer{}
		tool := New(dialer, t.TempDir())
		out, _ := tool.Invoke(context.Background(), args)
		if !strings.Contains(out, "identity_required") || dialer.calls != 0 {
			t.Fatalf("identity bypass: %s", out)
		}
		out, _ = tool.Invoke(browserTestContext(), args)
		if dialer.calls != 1 || strings.Contains(out, "invalid_arguments") {
			t.Fatalf("empty value rejected: %s", out)
		}
	}
}
func TestToolMetadataAndMissingConfigurationDoNotOpenBrowser(t *testing.T) {
	tool := New(nil, t.TempDir())
	if tool.Name() != "browser" || tool.(interface{ Group() string }).Group() != "browser" {
		t.Fatal("wrong catalog identity")
	}
	if tool.Schema()["additionalProperties"] != false {
		t.Fatal("schema allows forged identity")
	}
	out, err := tool.Invoke(context.Background(), map[string]any{"action": "snapshot"})
	if err != nil || !strings.Contains(out, `"code":"unavailable"`) || strings.Contains(out, `"ok":true`) {
		t.Fatalf("unconfigured tool: %s %v", out, err)
	}
}

func TestToolSnapshotNeedsNoModelAndCreatesNoWorkspace(t *testing.T) {
	base := t.TempDir()
	lease := &fixtureLease{}
	dialer := &fixtureDialer{lease: lease}
	browser := New(dialer, base)
	out, err := browser.Invoke(browserTestContext(), map[string]any{"action": "snapshot"})
	var result Result
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || !result.OK || result.Snapshot == nil || result.Snapshot.Text != "Visible page" || result.PageID != "page" {
		t.Fatalf("snapshot tool result: %s %v", out, err)
	}
	if dialer.acquires != 1 || lease.closed != 1 {
		t.Fatalf("operation lease count: acquire=%d close=%d", dialer.acquires, lease.closed)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("DOM read created a workspace: %v %v", entries, err)
	}
}

func TestResearchObservationProfileReachesBrowserScript(t *testing.T) {
	lease := &fixtureLease{}
	var request map[string]any
	lease.handler = func(method string, params, _ any) (bool, error) {
		if method == "Orka.observe" && params.(map[string]any)["operation"] == "snapshot" {
			request = params.(map[string]any)["request"].(map[string]any)
		}
		return false, nil
	}
	browser := New(&fixtureDialer{lease: lease}, t.TempDir())
	ctx := WithObservationProfile(browserTestContext(), ObservationResearch)
	if _, err := browser.Invoke(ctx, map[string]any{"action": "snapshot"}); err != nil {
		t.Fatal(err)
	}
	if request == nil {
		t.Fatal("snapshot request did not reach the browser script")
	}
	if got := int(request["text_limit"].(int)); got >= MaxSnapshotText {
		t.Fatalf("research text limit = %d, want below %d", got, MaxSnapshotText)
	}
	if got := int(request["element_limit"].(int)); got >= MaxSnapshotRefs {
		t.Fatalf("research element limit = %d, want below %d", got, MaxSnapshotRefs)
	}
	if got := int(request["byte_limit"].(int)); got >= MaxSnapshotBytes {
		t.Fatalf("research byte limit = %d, want below %d", got, MaxSnapshotBytes)
	}
}

func TestToolRepairsObservationWithoutReplayingMutation(t *testing.T) {
	first := &fixtureLease{handler: func(method string, params, _ any) (bool, error) {
		if method == "Orka.observe" && params.(map[string]any)["operation"] == "snapshot" {
			return true, NewActionError("observation_failed", "fixture observation failed")
		}
		return false, nil
	}}
	second := &fixtureLease{}
	dialer := &sequenceDialer{leases: []connectors.BrowserLease{first, second}}
	browser := New(dialer, t.TempDir())
	out, err := browser.Invoke(browserTestContext(), map[string]any{"action": "click", "selector": "button"})
	var result Result
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || !result.OK || result.Snapshot == nil || result.Recovery == nil {
		t.Fatalf("recovered browser result: %s %v", out, err)
	}
	if result.Action != "click" || result.Recovery.From != "observation_failed" || !result.Recovery.ActionAcknowledged {
		t.Fatalf("recovery receipt = %+v", result)
	}
	inputs := 0
	for _, method := range append(first.commands, second.commands...) {
		if method == "Input.dispatchMouseEvent" {
			inputs++
		}
	}
	if inputs != 2 || dialer.acquires != 2 || first.closed != 1 || second.closed != 1 {
		t.Fatalf("mutation replayed or leases leaked: inputs=%d acquires=%d closes=%d/%d", inputs, dialer.acquires, first.closed, second.closed)
	}
}

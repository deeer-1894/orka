package browsertool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFormAndFrameValidationBeforeBrowserAccess(t *testing.T) {
	cases := []map[string]any{
		{"action": "fill_form", "fields": []any{}},
		{"action": "fill_form", "fields": []any{nil}},
		{"action": "fill_form", "fields": []any{map[string]any{"selector": "input", "text": "x", "value": "y"}}},
		{"action": "fill_form", "fields": []any{map[string]any{"selector": "input", "text": "x", "expression": "evil"}}},
		{"action": "fill_form", "fields": []any{map[string]any{"selector": "input", "text": nil}}},
		{"action": "fill_form", "fields": []any{map[string]any{"selector": "input", "text": strings.Repeat("x", 9000)}, map[string]any{"selector": "input", "text": strings.Repeat("y", 9000)}}},
		{"action": "click", "selector": "button", "frame": []any{1}},
		{"action": "click", "ref": "e1", "snapshot_id": "s", "frame": []string{"iframe"}},
		{"action": "click", "selector": "button", "frame": []string{""}},
		{"action": "snapshot", "view": "arbitrary"},
	}
	for _, args := range cases {
		d := &countingDialer{}
		raw, _ := New(d, t.TempDir()).Invoke(browserTestContext(), args)
		if d.calls != 0 || !strings.Contains(raw, "invalid_arguments") {
			t.Fatalf("invalid form reached browser: %s", raw)
		}
	}
	for _, args := range []map[string]any{
		{"action": "fill_form", "fields": []any{map[string]any{"selector": "input", "text": ""}}},
		{"action": "fill", "selector": "input", "frame": []string{"#outer", "#inner"}, "text": "", "view": "full"},
	} {
		if _, err := parseRequest(args); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnknownFormDispatchStopsWithoutReplaying(t *testing.T) {
	lease := &fixtureLease{}
	calls := 0
	lease.handler = func(method string, params, out any) (bool, error) {
		if method != "Orka.act" {
			return false, nil
		}
		calls++
		if calls == 2 {
			return true, NewActionError("outcome_unknown", "Unconfirmed dispatch")
		}
		return true, json.Unmarshal([]byte(`{"result":{"value":{"ok":true}}}`), out)
	}
	x := "x"
	r, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "fill_form", Fields: []FormField{{Selector: "#a", Text: &x}, {Selector: "#b", Text: &x}, {Selector: "#c", Text: &x}}}, nil)
	if err == nil || r.Error.Code != "outcome_unknown" || r.Form.Completed != 1 || calls != 2 || lease.closed != 1 {
		t.Fatalf("unsafe partial form: %+v calls=%d err=%v", r, calls, err)
	}
}

func TestBrowserScriptsStayWithinTransportBounds(t *testing.T) {
	for name, source := range map[string]string{"helpers": domScript + observationScript, "snapshot": snapshotScript, "evaluate": evaluateScript} {
		if len(source) > MaxExpressionBytes-256 {
			t.Fatalf("%s exceeds bridge limit", name)
		}
	}
}

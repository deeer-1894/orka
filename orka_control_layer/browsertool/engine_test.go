package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/orka-oss/orka_control_layer/connectors"
	"math"
	"strings"
	"testing"
	"time"
)

type fixtureLease struct {
	commands []string
	closed   int
	failure  error
	closeErr error
	handler  func(string, any, any) (bool, error)
}

func (l *fixtureLease) Info() connectors.BrowserPageInfo {
	return connectors.BrowserPageInfo{LeaseID: "lease", PageID: "page", PageEpoch: 7}
}
func (l *fixtureLease) Close() error { l.closed++; return l.closeErr }
func (l *fixtureLease) Execute(_ context.Context, method string, params, out any) error {
	l.commands = append(l.commands, method)
	if l.failure != nil {
		return l.failure
	}
	if l.handler != nil {
		if handled, err := l.handler(method, params, out); handled {
			return err
		}
	}
	var response any
	switch method {
	case "Page.getFrameTree":
		response = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main"}}}
	case "Page.createIsolatedWorld":
		response = map[string]any{"executionContextId": 17}
	case "Runtime.callFunctionOn":
		response = map[string]any{"result": map[string]any{"value": map[string]any{"ok": true, "ready": true, "x": 21, "y": 34}}}
	case "Runtime.evaluate":
		response = map[string]any{"result": map[string]any{"type": "object", "value": map[string]any{"ok": true, "url": "https://fixture.test/", "title": "Fixture", "snapshot": map[string]any{"id": "snapshot", "text": "Visible page", "elements": []any{map[string]any{"ref": "e1", "tag": "button", "name": "Save"}}}}}}
	default:
		response = map[string]any{}
	}
	if out != nil {
		raw, _ := json.Marshal(response)
		return json.Unmarshal(raw, out)
	}
	return nil
}

type fixtureDialer struct {
	lease    *fixtureLease
	acquires int
}

func (d *fixtureDialer) Acquire(_ context.Context, id connectors.GUIIdentity, q, x time.Duration) (connectors.BrowserLease, error) {
	d.acquires++
	return d.lease, nil
}
func testIdentity() connectors.GUIIdentity {
	return connectors.GUIIdentity{OwnerID: "alice", ConversationID: "conv", RunID: "run"}
}

func TestEngineSnapshotUsesOneScopedLeaseAndIsolatedWorld(t *testing.T) {
	lease := &fixtureLease{}
	dialer := &fixtureDialer{lease: lease}
	result, err := NewEngine(dialer).Run(context.Background(), testIdentity(), Request{Action: "snapshot"}, nil)
	if err != nil || !result.OK || result.Snapshot == nil || result.Snapshot.Text != "Visible page" {
		t.Fatalf("snapshot=%+v err=%v", result, err)
	}
	if dialer.acquires != 1 || lease.closed != 1 || result.PageID != "page" || result.PageEpoch != 7 {
		t.Fatal("lease/scope not retained", result)
	}
	if len(lease.commands) != 3 || lease.commands[0] != "Page.getFrameTree" || lease.commands[1] != "Page.createIsolatedWorld" || lease.commands[2] != "Runtime.evaluate" {
		t.Fatal(lease.commands)
	}
}

func TestEngineRejectsUnsafeRequestsBeforeLease(t *testing.T) {
	for _, req := range []Request{{Action: "open", URL: "file:///etc/passwd"}, {Action: "open", URL: "https://user:secret@example.test"}, {Action: "click", Ref: "e1"}, {Action: "press", Selector: "button", Key: "Unknown"}, {Action: "wait", Condition: "document.readyState"}, {Action: "evaluate", Expression: strings.Repeat("x", MaxExpressionBytes+1)}} {
		dialer := &fixtureDialer{lease: &fixtureLease{}}
		result, err := NewEngine(dialer).Run(context.Background(), testIdentity(), req, nil)
		if err == nil || result.OK || dialer.acquires != 0 {
			t.Fatalf("request was not rejected before lease: %+v", req)
		}
	}
}

func TestEngineClickDoesNotReplayUnknownDispatch(t *testing.T) {
	lease := &fixtureLease{}
	lease.handler = func(method string, params, out any) (bool, error) {
		if method == "Input.dispatchMouseEvent" {
			return true, &connectors.BrowserError{Code: "outcome_unknown", Message: "private transport diagnostic"}
		}
		return false, nil
	}
	result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "click", Selector: "button"}, nil)
	if err == nil || result.OK || result.Error.Code != "outcome_unknown" || lease.closed != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	calls := 0
	for _, method := range lease.commands {
		if method == "Input.dispatchMouseEvent" {
			calls++
		}
	}
	if calls != 1 {
		t.Fatal("unacknowledged input was replayed", lease.commands)
	}
	if strings.Contains(err.Error(), "private transport diagnostic") {
		t.Fatal("raw transport diagnostic escaped public error")
	}
}

func TestEngineCleanupFailureIsUnknown(t *testing.T) {
	lease := &fixtureLease{closeErr: errors.New("cleanup failed")}
	result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "snapshot"}, nil)
	if err == nil || result.OK || result.Error.Code != "outcome_unknown" || lease.closed != 1 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestEngineEvaluationLimitRemainsDistinctAndSourceIsArgument(t *testing.T) {
	source := "document.title = 'new'; ({answer: 42})"
	for _, code := range []string{"output_limit", "script_error"} {
		t.Run(code, func(t *testing.T) {
			lease := &fixtureLease{}
			lease.handler = func(method string, params, out any) (bool, error) {
				if method != "Runtime.callFunctionOn" {
					return false, nil
				}
				raw, _ := json.Marshal(params)
				var call struct {
					Declaration string `json:"functionDeclaration"`
					Arguments   []struct {
						Value string `json:"value"`
					} `json:"arguments"`
					ContextID int64 `json:"executionContextId"`
				}
				if err := json.Unmarshal(raw, &call); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(call.Declaration, source) || len(call.Arguments) != 1 || call.Arguments[0].Value != source || call.ContextID != 17 {
					t.Fatal("source not passed separately into approved context")
				}
				encoded, _ := json.Marshal(map[string]any{"result": map[string]any{"value": map[string]any{"ok": false, "error": map[string]any{"code": code, "message": "bounded failure"}}}})
				return true, json.Unmarshal(encoded, out)
			}
			result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "evaluate", Expression: source}, nil)
			if err == nil || result.Error.Code != code {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

type scopedFiles struct {
	t      *testing.T
	lease  *fixtureLease
	called bool
}

func (f *scopedFiles) Execute(_ context.Context, s Session, _ Request) ([]FileResult, error) {
	f.called = true
	if s.Lease != f.lease || s.ContextID != 17 || s.Identity != testIdentity() || f.lease.closed != 0 {
		f.t.Fatal("files escaped current approved lease")
	}
	return []FileResult{{Path: "page.png", Size: 100}}, nil
}
func TestEngineFilesShareApprovedLease(t *testing.T) {
	lease := &fixtureLease{}
	files := &scopedFiles{t: t, lease: lease}
	result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "screenshot", Path: "page.png"}, files)
	if err != nil || !result.OK || !files.called || lease.closed != 1 || len(result.Files) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestEngineRejectsNonfiniteScrollAndMalformedIdentity(t *testing.T) {
	for _, amount := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		d := &fixtureDialer{lease: &fixtureLease{}}
		_, err := NewEngine(d).Run(context.Background(), testIdentity(), Request{Action: "scroll", Amount: amount}, nil)
		if err == nil || d.acquires != 0 {
			t.Fatal("nonfinite amount acquired lease", amount)
		}
	}
	for _, value := range []string{" ", strings.Repeat("x", 257)} {
		identity := testIdentity()
		identity.OwnerID = value
		d := &fixtureDialer{lease: &fixtureLease{}}
		_, err := NewEngine(d).Run(context.Background(), identity, Request{Action: "snapshot"}, nil)
		if err == nil || d.acquires != 0 {
			t.Fatal("invalid identity acquired lease")
		}
	}
}

func TestEngineMutationWithoutFinalObservationIsUnknown(t *testing.T) {
	lease := &fixtureLease{}
	frames := 0
	lease.handler = func(method string, params, out any) (bool, error) {
		if method == "Page.getFrameTree" {
			frames++
			if frames == 2 {
				return true, context.DeadlineExceeded
			}
		}
		return false, nil
	}
	result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), testIdentity(), Request{Action: "click", Selector: "button"}, nil)
	if err == nil || result.OK || result.Error.Code != "outcome_unknown" {
		t.Fatalf("completed click hidden by observation failure: %+v %v", result, err)
	}
	clicks := 0
	for _, method := range lease.commands {
		if method == "Input.dispatchMouseEvent" {
			clicks++
		}
	}
	if clicks != 2 || lease.closed != 1 {
		t.Fatal("mutation replayed or lease leaked", lease.commands)
	}
}

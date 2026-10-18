package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/browsertool"
	"github.com/orka-oss/orka_control_layer/connectors"
)

// Exercise the production browser tool, engine, receipt encoder, Eino adapter
// and plan tool. Only the remote CDP lease is substituted; no handcrafted tool
// receipt bypasses the failure/observation pipeline.
type planBoundaryBrowser struct {
	stale  bool
	clicks int
	url    string
}

func (b *planBoundaryBrowser) Acquire(context.Context, connectors.GUIIdentity, time.Duration, time.Duration) (connectors.BrowserLease, error) {
	return b, nil
}
func (*planBoundaryBrowser) Info() connectors.BrowserPageInfo {
	return connectors.BrowserPageInfo{LeaseID: "lease", PageID: "page", PageEpoch: 7}
}
func (*planBoundaryBrowser) Close() error { return nil }
func (b *planBoundaryBrowser) Execute(_ context.Context, method string, params, out any) error {
	var response any = map[string]any{}
	value := func(v any) { response = map[string]any{"result": map[string]any{"value": v}} }
	switch method {
	case "Orka.getPageState":
	case "Page.getFrameTree":
		response = map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main"}}}
	case "Page.createIsolatedWorld":
		response = map[string]any{"executionContextId": 17}
	case "Orka.act":
		if b.stale {
			b.stale = false
			value(map[string]any{"ok": false, "error": map[string]any{"code": "stale_ref", "message": "Reference belongs to an old document; observe again."}})
		} else {
			value(map[string]any{"ok": true, "x": 20, "y": 30})
		}
	case "Input.dispatchMouseEvent":
		if params.(map[string]any)["type"] == "mouseReleased" {
			b.clicks++
			b.url = "https://required.example/accepted"
		}
	case "Orka.observe":
		switch params.(map[string]any)["operation"] {
		case "settle", "wait":
			value(map[string]any{"ok": true, "ready": true, "revision": 1})
		case "commit_observation":
			value(map[string]any{"ok": true})
		case "snapshot":
			value(map[string]any{"ok": true, "url": b.url, "snapshot": map[string]any{
				"id": "current", "text": "Form ready; accepted after click", "elements": []any{map[string]any{"ref": "e1", "tag": "button", "name": "Submit"}},
			}})
		default:
			return fmt.Errorf("unexpected observation: %v", params)
		}
	default:
		return fmt.Errorf("unexpected CDP method: %s", method)
	}
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func TestBrowserBoundaryFirstStaleRefSnapshotClickDone(t *testing.T) {
	p := &planTracker{}
	setRecoveryStep(p, "active")
	ctx := withPlanTracker(context.Background(), p)
	ctx = connectors.WithGUIIdentity(ctx, connectors.GUIIdentity{OwnerID: "test", ConversationID: "conv", RunID: "run"})
	browser := &planBoundaryBrowser{stale: true, url: "https://required.example/form"}
	tool := EinoTool(browsertool.New(browser, t.TempDir()))
	invoke := func(args string) (browsertool.Result, planBrowserReceipt) {
		t.Helper()
		out, err := tool.InvokableRun(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(out, "\n")
		var result browsertool.Result
		if err := json.Unmarshal([]byte(lines[0]), &result); err != nil {
			t.Fatalf("receipt: %s: %v", out, err)
		}
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "[Plan browser evidence] ") {
			t.Fatalf("missing evidence: %s", out)
		}
		var note struct {
			Receipt planBrowserReceipt `json:"receipt"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "[Plan browser evidence] ")), &note); err != nil {
			t.Fatal(err)
		}
		return result, note.Receipt
	}
	first, _ := invoke(`{"action":"click","ref":"old","snapshot_id":"obsolete"}`)
	if first.OK || first.Error == nil || first.Error.Code != "stale_ref" || first.URL != "" || browser.clicks != 0 {
		t.Fatalf("first stale target dispatched or carried fabricated URL: %+v; clicks=%d", first, browser.clicks)
	}
	observed, snapshot := invoke(`{"action":"snapshot"}`)
	if !observed.OK || observed.Snapshot == nil {
		t.Fatal(observed)
	}
	clicked, accepted := invoke(`{"action":"click","ref":"e1","snapshot_id":"current"}`)
	if !clicked.OK || browser.clicks != 1 || clicked.URL != "https://required.example/accepted" {
		t.Fatal(clicked, browser.clicks)
	}
	out, err := (planTool{}).Invoke(ctx, map[string]any{"steps": []any{map[string]any{
		"id": "a", "title": "verify required interaction", "status": "done",
		"reason":       "Fresh form snapshot, then one acknowledged click with the accepted-page receipt",
		"evidence_ids": []any{snapshot.ID, accepted.ID},
	}}})
	if err != nil || !p.completed() || !strings.Contains(out, `"unfinished":[]`) {
		t.Fatalf("real receipt recovery still requires another continue: %s %v", out, err)
	}
}

func TestRealBrowserPlanFirstStaleRefSnapshotClickDone(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("requires isolated ORKA_BROWSER_TEST_WS")
	}
	var submissions atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/accepted" {
			submissions.Add(1)
			fmt.Fprint(w, `<!doctype html><title>Accepted</title><p>Submission accepted exactly once</p>`)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Form</title><button onclick="location.href='/accepted'">Submit</button>`)
	}))
	defer fixture.Close()
	ctx := connectors.WithGUIIdentity(context.Background(), connectors.GUIIdentity{OwnerID: "plan-recovery-fixture", ConversationID: fmt.Sprint(time.Now().UnixNano()), RunID: "recovery"})
	base := browsertool.New(connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN")), t.TempDir())
	// The shared browser already has a page, but this new plan step has no prior
	// observations. Its FIRST call uses an obsolete snapshot reference.
	setup, err := base.Invoke(ctx, map[string]any{"action": "open", "url": fixture.URL})
	if err != nil {
		t.Fatal(err)
	}
	var initial browsertool.Result
	if err := json.Unmarshal([]byte(setup), &initial); err != nil || !initial.OK {
		t.Fatalf("setup: %s %v", setup, err)
	}
	p := &planTracker{}
	setRecoveryStep(p, "active")
	ctx = withPlanTracker(ctx, p)
	tool := EinoTool(base)
	invoke := func(args map[string]any) (browsertool.Result, string) {
		t.Helper()
		raw, _ := json.Marshal(args)
		out, err := tool.InvokableRun(ctx, string(raw))
		if err != nil {
			t.Fatal(err)
		}
		var result browsertool.Result
		if err := json.Unmarshal([]byte(strings.Split(out, "\n")[0]), &result); err != nil {
			t.Fatal(out, err)
		}
		state := p.checkpoint().Browser["id:a"]
		return result, state.Receipts[len(state.Receipts)-1].ID
	}
	failed, _ := invoke(map[string]any{"action": "click", "ref": "e1", "snapshot_id": "obsolete"})
	if failed.OK || failed.Error == nil || failed.Error.Code != "stale_ref" || failed.URL != "" || submissions.Load() != 0 {
		t.Fatalf("first browser call did not reproduce undispatched stale ref: %+v", failed)
	}
	observed, observationID := invoke(map[string]any{"action": "snapshot"})
	if !observed.OK || observed.Snapshot == nil {
		t.Fatal(observed)
	}
	ref := ""
	for _, el := range observed.Snapshot.Elements {
		if el.Name == "Submit" {
			ref = el.Ref
			break
		}
	}
	if ref == "" {
		t.Fatal("real snapshot missing Submit target", observed)
	}
	clicked, clickID := invoke(map[string]any{"action": "click", "ref": ref, "snapshot_id": observed.Snapshot.ID})
	if !clicked.OK || clicked.URL != fixture.URL+"/accepted" || submissions.Load() != 1 {
		t.Fatalf("real click did not navigate exactly once: %+v submissions=%d", clicked, submissions.Load())
	}
	out, err := (planTool{}).Invoke(ctx, map[string]any{"steps": []any{map[string]any{
		"id": "a", "title": "verify required interaction", "status": "done",
		"reason":       "Fresh browser snapshot followed by one click; observed the accepted page",
		"evidence_ids": []any{observationID, clickID},
	}}})
	if err != nil || !p.completed() || !strings.Contains(out, `"unfinished":[]`) {
		t.Fatalf("real browser workflow needs another continue: %s %v", out, err)
	}
}

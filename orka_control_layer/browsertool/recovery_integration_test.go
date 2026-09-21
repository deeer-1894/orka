package browsertool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

func TestRealBrowserRecovery(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("requires isolated ORKA_BROWSER_TEST_WS")
	}
	var hangs atomic.Int32
	finished := make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/hang" {
			hangs.Add(1)
			select {
			case <-r.Context().Done():
			case <-finished:
			}
			return
		}
		if r.URL.Path == "/document" {
			fmt.Fprint(w, `<!doctype html><title>New document</title><button onclick="this.textContent='Done'">Continue document</button>`)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Recovery fixture</title><input id="draft" value="keep me"><button id="route">Route</button><button id="next">Next</button><a id="hang" href="/hang">Unreachable</a><button id="document" onclick="setTimeout(()=>location.href='/document',180)">New document</button><script>
		document.querySelector('#route').onclick=()=>setTimeout(()=>{history.pushState({},'', '/changed');document.querySelector('#next').outerHTML='<button id="next" onclick="this.textContent=\'Done\'">Continue</button>'},180);
		</script>`)
	}))
	defer func() { close(finished); fixture.Close() }()
	engine := NewEngine(connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN")))
	who := connectors.GUIIdentity{OwnerID: "recovery-fixture", ConversationID: fmt.Sprint(time.Now().UnixNano()), RunID: "recovery"}
	run := func(req Request) Result {
		t.Helper()
		r, err := engine.Run(context.Background(), who, req, nil)
		if err != nil || !r.OK {
			t.Fatalf("%s: %+v %v", req.Action, r, err)
		}
		return r
	}
	t.Run("delayed_SPA_receipt_refs", func(t *testing.T) {
		run(Request{Action: "open", URL: fixture.URL})
		r := run(Request{Action: "click", Selector: "#route"})
		if !strings.HasSuffix(r.URL, "/changed") {
			t.Fatalf("receipt returned before SPA navigation: %s", r.URL)
		}
		time.Sleep(250 * time.Millisecond) // Model's next turn, after the delayed route.
		for _, el := range r.Snapshot.Elements {
			if el.Name == "Continue" {
				next := run(Request{Action: "click", Ref: el.Ref, SnapshotID: r.Snapshot.ID})
				if !strings.Contains(next.Snapshot.Text, "Done") {
					t.Fatal("returned reference did not target the settled document")
				}
				return
			}
		}
		t.Fatal("settled receipt missing Continue reference")
	})
	t.Run("navigation_timeout_preserves_context", func(t *testing.T) {
		before := run(Request{Action: "open", URL: fixture.URL})
		failed, err := engine.Run(context.Background(), who, Request{Action: "open", URL: fixture.URL + "/hang", TimeoutMS: 500}, nil)
		if err == nil || failed.OK {
			t.Fatal("hanging navigation reported success")
		}
		after := run(Request{Action: "snapshot"})
		if after.PageID != before.PageID || after.URL == "about:blank" {
			t.Fatalf("navigation timeout discarded context: before=%s after=%+v", before.PageID, after)
		}
		check := run(Request{Action: "evaluate", Expression: "document.querySelector('#draft')?.value"})
		if check.Value != "keep me" {
			t.Fatalf("draft lost after precommit timeout: %v", check.Value)
		}
	})
	t.Run("delayed_document_navigation", func(t *testing.T) {
		run(Request{Action: "open", URL: fixture.URL})
		r := run(Request{Action: "click", Selector: "#document"})
		if !strings.HasSuffix(r.URL, "/document") || r.Title != "New document" {
			t.Fatalf("old document receipt escaped: %+v", r)
		}
		for _, el := range r.Snapshot.Elements {
			if el.Name == "Continue document" {
				run(Request{Action: "click", Ref: el.Ref, SnapshotID: r.Snapshot.ID})
				return
			}
		}
		t.Fatal("new document reference missing")
	})
	t.Run("acknowledged_click_stuck_navigation", func(t *testing.T) {
		before := run(Request{Action: "open", URL: fixture.URL})
		requests := hangs.Load()
		started := time.Now()
		failed, err := engine.Run(context.Background(), who, Request{Action: "click", Selector: "#hang"}, nil)
		if elapsed := time.Since(started); elapsed > 6*time.Second {
			t.Errorf("observation got stuck behind pending navigation: %v", elapsed)
		}
		if err == nil || failed.Error.Code != "observation_failed" || failed.Snapshot != nil {
			t.Fatalf("stuck navigation fabricated a final receipt: %+v %v", failed, err)
		}
		after := run(Request{Action: "snapshot"})
		if after.PageID != before.PageID || after.URL != before.URL {
			t.Fatalf("click's pending navigation lost context: %+v", after)
		}
		if hangs.Load()-requests != 1 {
			t.Fatal("click navigation was replayed")
		}
	})
	t.Run("real_GUI_handoff_invalidates_refs", func(t *testing.T) {
		before := run(Request{Action: "open", URL: fixture.URL})
		var target Element
		for _, el := range before.Snapshot.Elements {
			if el.Name == "Route" {
				target = el
				break
			}
		}
		if target.Ref == "" {
			t.Fatal("missing initial reference")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := fixtureGUIHandoff(ctx, endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN"), who); err != nil {
			t.Fatal(err)
		}
		stale, err := engine.Run(ctx, who, Request{Action: "click", Ref: target.Ref, SnapshotID: before.Snapshot.ID}, nil)
		if err == nil || stale.Error.Code != "stale_ref" {
			t.Fatalf("real GUI takeover accepted old reference: %+v %v", stale, err)
		}
		after := run(Request{Action: "snapshot"})
		if after.PageID != before.PageID || after.PageEpoch <= before.PageEpoch {
			t.Fatalf("GUI handoff lost page or epoch: %+v", after)
		}
	})
}

// Shared by integration fixtures that need a real GUI handoff. Production does
// not expose this test-server endpoint; no client-supplied epoch is trusted.
func fixtureGUIHandoff(ctx context.Context, endpoint, token string, who connectors.GUIIdentity) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}
	u.Path, u.RawQuery = "/fixture/gui-handoff", ""
	body, err := json.Marshal(who)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("isolated GUI fixture returned HTTP %d", response.StatusCode)
	}
	return nil
}

package browsertool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/pathsafe"
)

//go:embed testdata/workbench.html
var contractPage string

// This opt-in contract uses a separate authenticated bridge and disposable
// Chromium sessions. It performs no model calls and never attaches to user tabs.
func TestRealBrowserContract(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("set ORKA_BROWSER_TEST_WS for isolated real Chromium contract")
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, contractPage)
		case "/frame":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<button>Frame button</button>")
		case "/protected.csv":
			c, err := r.Cookie("fixture_auth")
			if err != nil || c.Value != "contract" {
				http.Error(w, "login required", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/csv")
			fmt.Fprint(w, "month,revenue\nApril,84000\nMay,96000\n")
		case "/maximum":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprint(MaxFileBytes))
			fmt.Fprint(w, strings.Repeat("x", MaxFileBytes))
		case "/oversized":
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, strings.Repeat("x", MaxFileBytes+1))
		default:
			http.NotFound(w, r)
		}
	}))
	fixture.Listener.Close()
	listener, listenErr := net.Listen("tcp4", "0.0.0.0:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	fixture.Listener = listener
	fixture.Start()
	defer fixture.Close()
	host := os.Getenv("ORKA_BROWSER_TEST_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	fixtureURL := fmt.Sprintf("http://%s:%d", host, listener.Addr().(*net.TCPAddr).Port)
	base := t.TempDir()
	identity := connectors.GUIIdentity{OwnerID: "browser-contract@example.test", ConversationID: fmt.Sprintf("contract-%d", time.Now().UnixNano()), RunID: "run-contract"}
	engine := NewEngine(connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN")))
	invoke := func(who connectors.GUIIdentity, req Request) (Result, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		return engine.Run(ctx, who, req, NewFiles(base, who))
	}
	run := func(req Request) Result {
		t.Helper()
		result, err := invoke(identity, req)
		if err != nil || !result.OK {
			t.Fatalf("%s failed: result=%+v error=%v", req.Action, result, err)
		}
		return result
	}
	reject := func(req Request, code string) {
		t.Helper()
		result, err := invoke(identity, req)
		if result.OK && err == nil {
			t.Fatalf("%s unexpectedly succeeded: %+v", req.Action, result)
		}
		payload, _ := json.Marshal(result)
		if code != "" && !strings.Contains(string(payload)+fmt.Sprint(err), code) {
			t.Fatalf("expected %s: %s %v", code, payload, err)
		}
	}
	opened := run(Request{Action: "open", URL: fixtureURL})
	if opened.Snapshot == nil {
		t.Fatal("navigation did not return snapshot")
	}
	payload, _ := json.Marshal(opened)
	for _, text := range []string{"84000", "Customer name", "Apply scenario"} {
		if !strings.Contains(string(payload), text) {
			t.Fatalf("snapshot missing %s", text)
		}
	}
	if strings.Contains(string(payload), "fixture-password-must-not-appear") {
		t.Fatal("password leaked into observation")
	}
	if !opened.Snapshot.PixelContent || len(opened.Snapshot.Frames) == 0 {
		t.Fatal("pixel/frame boundaries not reported")
	}
	var apply Element
	for _, el := range opened.Snapshot.Elements {
		if el.Name == "Apply scenario" {
			apply = el
			break
		}
	}
	if apply.Ref == "" {
		t.Fatal("apply ref missing")
	}
	run(Request{Action: "fill", Selector: "#customer", Text: "Nimbus QA"})
	run(Request{Action: "select", Selector: "#region", Value: "south"})
	applied := run(Request{Action: "click", Selector: "#apply"})
	if applied.Snapshot == nil || !strings.Contains(applied.Snapshot.Text, "Applied") {
		t.Fatalf("reactive form did not update: %+v", applied.Snapshot)
	}
	run(Request{Action: "wait", Condition: "visible", Selector: "#apply"})
	run(Request{Action: "press", Selector: "#customer", Key: "Enter"})
	run(Request{Action: "wait", Condition: "text", Text: "Entered", Selector: "#status"})
	run(Request{Action: "scroll", Direction: "down", Amount: 500})
	reject(Request{Action: "click", Selector: ".duplicate"}, "ambiguous_selector")
	reject(Request{Action: "click", Selector: "#disabled"}, "not_interactable")
	reject(Request{Action: "click", Selector: "#covered"}, "not_interactable")
	run(Request{Action: "click", Selector: "#replace"})
	reject(Request{Action: "click", Ref: apply.Ref, SnapshotID: opened.Snapshot.ID}, "stale_ref")
	evaluated := run(Request{Action: "evaluate", Expression: "document.querySelector('#status').textContent = 'Script updated'; ({value: 42, title: document.title})"})
	value, _ := json.Marshal(evaluated.Value)
	if !strings.Contains(string(value), "42") {
		t.Fatalf("evaluation result lost: %s", value)
	}
	reject(Request{Action: "evaluate", Expression: "(()=>{throw new Error('fixture exception')})()"}, "script_error")
	reject(Request{Action: "evaluate", Expression: "'x'.repeat(70000)"}, "output_limit")
	snapshot := run(Request{Action: "snapshot"})
	var shadow Element
	for _, el := range snapshot.Snapshot.Elements {
		if el.Name == "Shadow action" {
			shadow = el
			break
		}
	}
	if shadow.Ref == "" {
		t.Fatal("open shadow button missing")
	}
	run(Request{Action: "click", Ref: shadow.Ref, SnapshotID: snapshot.Snapshot.ID})
	shot := run(Request{Action: "screenshot", Path: "evidence/page.png"})
	if len(shot.Files) != 1 || shot.Files[0].Size < 100 {
		t.Fatal("missing screenshot artifact")
	}
	download := run(Request{Action: "download", URL: fixtureURL + "/protected.csv", Path: "reports/data.csv"})
	if len(download.Files) != 1 || download.Files[0].MIME != "text/csv" {
		t.Fatalf("bad download metadata: %+v", download.Files)
	}
	root, err := pathsafe.EnsureSession(base, identity.OwnerID, identity.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(root + "/reports/data.csv")
	if err != nil || string(data) != "month,revenue\nApril,84000\nMay,96000\n" {
		t.Fatalf("download bytes mismatch: %q %v", data, err)
	}
	reject(Request{Action: "download", URL: fixtureURL + "/protected.csv", Path: "reports/data.csv"}, "file_exists")
	run(Request{Action: "download", URL: fixtureURL + "/protected.csv", Path: "reports/data.csv", Mode: "replace"})
	blob := run(Request{Action: "evaluate", Expression: "URL.createObjectURL(new Blob(['hello browser export'],{type:'text/plain'}))"})
	blobURL, ok := blob.Value.(string)
	if !ok || !strings.HasPrefix(blobURL, "blob:") {
		t.Fatalf("bad blob URL: %#v", blob.Value)
	}
	run(Request{Action: "download", URL: blobURL, Path: "reports/export.txt"})
	maximum := run(Request{Action: "download", URL: fixtureURL + "/maximum", Path: "reports/maximum.bin"})
	if len(maximum.Files) != 1 || maximum.Files[0].Size != MaxFileBytes {
		t.Fatalf("16 MiB transfer incomplete: %+v", maximum.Files)
	}
	reject(Request{Action: "download", URL: fixtureURL + "/oversized", Path: "reports/too-large.bin"}, "output_limit")
	if _, err := os.Stat(root + "/reports/too-large.bin"); !os.IsNotExist(err) {
		t.Fatal("failed download left partial file")
	}
	other := identity
	other.ConversationID += "-other"
	if result, err := invoke(other, Request{Action: "open", URL: fixtureURL}); err != nil || !result.OK {
		t.Fatalf("second conversation: %+v %v", result, err)
	}
	if result, err := invoke(other, Request{Action: "download", URL: fixtureURL + "/protected.csv", Path: "private.csv"}); result.OK && err == nil {
		t.Fatal("browser cookie leaked between conversations")
	}
	otherOwner := identity
	otherOwner.OwnerID = "other-browser-contract@example.test"
	if result, err := invoke(otherOwner, Request{Action: "open", URL: fixtureURL}); err != nil || !result.OK {
		t.Fatalf("second owner: %+v %v", result, err)
	}
	if result, err := invoke(otherOwner, Request{Action: "download", URL: fixtureURL + "/protected.csv", Path: "private.csv"}); result.OK && err == nil {
		t.Fatal("browser cookie leaked between owners")
	}
	run(Request{Action: "open", URL: fixtureURL + "/?next=1"})
	reject(Request{Action: "click", Ref: shadow.Ref, SnapshotID: snapshot.Snapshot.ID}, "stale_ref")
	reject(Request{Action: "open", URL: "file:///etc/passwd"}, "")
	beforeCancel := run(Request{Action: "snapshot"})
	startedCancel := time.Now()
	rejected, cancelErr := invoke(identity, Request{Action: "evaluate", Expression: "for (;;) {}", TimeoutMS: 300})
	if rejected.OK || cancelErr == nil || time.Since(startedCancel) > 10*time.Second {
		t.Fatalf("unbounded or falsely successful cancellation: %+v %v", rejected, cancelErr)
	}
	recovered := run(Request{Action: "open", URL: fixtureURL})
	if recovered.PageID == beforeCancel.PageID {
		t.Fatal("uncertain page was reused after infinite script cancellation")
	}
	t.Log("real Chromium passed DOM/ref/CSS/form/JS/screenshot/authenticated+blob downloads/isolation contracts")
}

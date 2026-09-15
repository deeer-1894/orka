package browsertool

import (
	"context"
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
)

// The DOM contract is exercised in Chromium, not a mock DOM implementation.
// Like the parent's broader contract this uses only the opt-in isolated bridge.
func TestRealSnapshotOptionsAndWhitespaceRedaction(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("set ORKA_BROWSER_TEST_WS for isolated Chromium snapshot contract")
	}
	var options strings.Builder
	for i := 0; i < 43; i++ {
		fmt.Fprintf(&options, "<option value='v%d'>Choice %d</option>", i, i)
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><title>Snapshot contract</title><label for="field">Private note</label><textarea id="field" oninput="document.querySelector('#echo').textContent=this.value"></textarea><div id="echo"></div><div contenteditable="true">preexisting-private-editable</div><label for="choice">Region</label><select id="choice">%s</select>`, options.String())
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Listener = listener
	fixture.Start()
	defer fixture.Close()
	target := "http://host.docker.internal:" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	who := connectors.GUIIdentity{OwnerID: "snapshot-contract@example.test", ConversationID: fmt.Sprintf("snapshot-%d", time.Now().UnixNano()), RunID: "snapshot-run"}
	dialer := &snapshotEpochDialer{BrowserDialer: connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN"))}
	engine := NewEngine(dialer)
	run := func(req Request) Result {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		defer cancel()
		result, err := engine.Run(ctx, who, req, nil)
		if err != nil || !result.OK {
			t.Fatalf("%s: %+v %v", req.Action, result, err)
		}
		return result
	}
	opened := run(Request{Action: "open", URL: target})
	payload, _ := json.Marshal(opened)
	if strings.Contains(string(payload), "preexisting-private-editable") {
		t.Fatal("contenteditable value escaped snapshot")
	}
	if opened.Snapshot == nil {
		t.Fatal("missing snapshot")
	}
	var selectNode *Element
	for i := range opened.Snapshot.Elements {
		if opened.Snapshot.Elements[i].Tag == "select" {
			selectNode = &opened.Snapshot.Elements[i]
			break
		}
	}
	if selectNode == nil || len(selectNode.Options) != 40 || !opened.Snapshot.Omitted {
		t.Fatalf("bounded select options missing: %+v", opened.Snapshot)
	}
	if selectNode.Options[0].Value != "v0" || selectNode.Options[0].Label != "Choice 0" || !selectNode.Options[0].Selected {
		t.Fatalf("option state missing: %+v", selectNode.Options[0])
	}
	result := run(Request{Action: "fill", Selector: "#field", Text: "private  marker\n has whitespace"})
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private marker has whitespace") || !strings.Contains(string(raw), "[redacted]") {
		t.Fatalf("whitespace echo escaped redaction: %s", raw)
	}
	// Mutating reactive forms must not require or report the input value itself.
	if strings.Contains(string(raw), "private  marker") {
		t.Fatal("raw input value escaped redaction")
	}
	// GUI handoff changes only the transport epoch while the document and its
	// isolated world survive. Simulate that signal without calling a GUI model.
	var fieldRef string
	for _, element := range result.Snapshot.Elements {
		if element.Tag == "textarea" {
			fieldRef = element.Ref
			break
		}
	}
	if fieldRef == "" {
		t.Fatal("textarea reference missing")
	}
	previous := result
	for _, transition := range []string{"gui_epoch", "new_run"} {
		t.Run(transition, func(t *testing.T) {
			if transition == "gui_epoch" {
				dialer.epochOffset++
			} else {
				who.RunID = "snapshot-next-run"
			}
			stale, err := engine.Run(context.Background(), who, Request{Action: "click", Ref: fieldRef, SnapshotID: previous.Snapshot.ID}, nil)
			if err == nil || stale.Error == nil || stale.Error.Code != "stale_ref" {
				t.Fatalf("old scope reference remained usable: %+v %v", stale, err)
			}
			observed := run(Request{Action: "snapshot"})
			if observed.PageID != previous.PageID || observed.PageEpoch <= previous.PageEpoch {
				t.Fatalf("expected surviving page and changed epoch: before=%+v after=%+v", previous, observed)
			}
			payload, _ := json.Marshal(observed)
			if strings.Contains(string(payload), "private marker has whitespace") || !strings.Contains(string(payload), "[redacted]") {
				t.Fatalf("%s leaked a surviving document's input echo: %s", transition, payload)
			}
			previous = observed
		})
	}
}

// Only the GUI handoff epoch notification is simulated. Commands still execute
// against the real Chromium document, through its authenticated scoped lease.
type snapshotEpochDialer struct {
	connectors.BrowserDialer
	epochOffset int64
}

func (d *snapshotEpochDialer) Acquire(ctx context.Context, identity connectors.GUIIdentity, queue, execution time.Duration) (connectors.BrowserLease, error) {
	lease, err := d.BrowserDialer.Acquire(ctx, identity, queue, execution)
	if err != nil {
		return nil, err
	}
	return snapshotEpochLease{BrowserLease: lease, offset: d.epochOffset}, nil
}

type snapshotEpochLease struct {
	connectors.BrowserLease
	offset int64
}

func (l snapshotEpochLease) Info() connectors.BrowserPageInfo {
	info := l.BrowserLease.Info()
	info.PageEpoch += l.offset
	return info
}

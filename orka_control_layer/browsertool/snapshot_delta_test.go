package browsertool

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

func TestSnapshotDeltaScript(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for observation script unit tests")
	}
	out, err := exec.Command("node", "snapshot_delta_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("observation regression: %v\n%s", err, out)
	}
}

// Uses the existing opt-in isolated bridge, never a production/user browser.
func TestRealSnapshotBoundedDelta(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("set ORKA_BROWSER_TEST_WS for isolated Chromium")
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>HN filter fixture</title><label><input id="filter" type="checkbox" onclick="document.querySelector('#status').textContent=this.checked?'Minimum: 5; median: 5.05':'Minimum: 0; median: 5.05'">Minimum score</label><p id="status">Minimum: 0; median: 5.05</p><div id="stories">`)
		for i := 0; i < 260; i++ {
			fmt.Fprintf(w, `<a href="#story-%d">Story %03d: browser observation</a><br>`, i, i)
		}
		fmt.Fprint(w, `</div><button id="late">After all links</button><div id="ordered"><p>A</p><p>B</p><p>A</p><p>old</p><p>C</p></div><textarea id="private" oninput="document.querySelector('#echo').textContent=this.value"></textarea><p id="echo"></p><iframe id="cross" sandbox srcdoc="<button>Secret cross origin</button>"></iframe>`)
		fmt.Fprint(w, `<input id="search" type="search" aria-label="Search documentation" oninput="document.querySelector('#search-echo').textContent=this.value"><p id="search-echo"></p><input id="credential-search" type="search" aria-label="API token" oninput="document.querySelector('#credential-echo').textContent=this.value"><p id="credential-echo"></p>`)
	}))
	fixture.Listener.Close()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Listener = listener
	fixture.Start()
	defer fixture.Close()
	host := os.Getenv("ORKA_BROWSER_TEST_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	target := fmt.Sprintf("http://%s:%d", host, listener.Addr().(*net.TCPAddr).Port)
	who := connectors.GUIIdentity{OwnerID: "snapshot-delta@example.test", ConversationID: fmt.Sprintf("snapshot-delta-%d", time.Now().UnixNano()), RunID: "snapshot-delta"}
	engine := NewEngine(connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN")))
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
	full := run(Request{Action: "open", URL: target})
	if full.Snapshot == nil || !full.Snapshot.Omitted || len(full.Snapshot.Elements) != 200 {
		t.Fatalf("fixture must exercise the 200-element limit: %+v", full.Snapshot)
	}
	delta := run(Request{Action: "click", Selector: "#filter"})
	if delta.Snapshot.Mode != "delta" || !strings.Contains(delta.Snapshot.Text, "Minimum: 5; median: 5.05") {
		t.Fatalf("small filter must return delta with statistics: %+v", delta.Snapshot)
	}
	var checked, late bool
	for _, e := range delta.Snapshot.Elements {
		checked = checked || e.Role == "checkbox" && e.Checked
		late = late || e.Name == "After all links"
	}
	if !checked || !late {
		t.Fatal("current filter or late control missing")
	}
	before, _ := json.Marshal(full)
	after, _ := json.Marshal(delta)
	if len(after)*4 >= len(before) {
		t.Fatalf("insufficient reduction: %d -> %d bytes", len(before), len(after))
	}
	t.Logf("260-link filter receipt: full=%d bytes, delta=%d bytes, reduction=%.1f%%", len(before), len(after), 100*(1-float64(len(after))/float64(len(before))))
	for _, f := range delta.Snapshot.Frames {
		if f.Supported || f.Handoff != "gui" {
			t.Fatal("cross-origin boundary changed", f)
		}
	}
	if len(delta.Snapshot.Frames) != 1 {
		t.Fatal("frame boundary missing")
	}
	// Elision must not revoke unchanged refs held by the caller, and repeated
	// observations must compare against the complete internal baseline.
	var storyRef string
	for _, e := range full.Snapshot.Elements {
		if e.Role == "link" {
			storyRef = e.Ref
			break
		}
	}
	for i := 0; i < 3; i++ {
		unchanged := run(Request{Action: "wait", Ref: storyRef, SnapshotID: full.Snapshot.ID, Condition: "visible"})
		if unchanged.Snapshot.Mode != "unchanged" || unchanged.Change.Observed {
			t.Fatal("elided ref or full internal baseline lost")
		}
	}
	run(Request{Action: "evaluate", Expression: `document.querySelector('#ordered').innerHTML='<p>B</p><p>A</p><p>A</p><p>new</p><p>C</p>'; null`})
	reordered := run(Request{Action: "wait", Selector: "#ordered", Condition: "visible"})
	if reordered.Snapshot.Mode != "delta" || !strings.Contains(reordered.Snapshot.Text, "B\nA\nA\nnew") {
		t.Fatal("mixed text reorder/replacement lost", reordered.Snapshot)
	}
	private := run(Request{Action: "fill", Selector: "#private", Text: "private  marker\nvalue"})
	raw, _ := json.Marshal(private)
	if strings.Contains(string(raw), "private marker value") || !strings.Contains(string(raw), "[redacted]") {
		t.Fatal("redaction lost")
	}
	search := run(Request{Action: "fill", Selector: "#search", Text: "PostgreSQL"})
	if !strings.Contains(search.Snapshot.Text, "PostgreSQL") {
		t.Fatal("public search query corrupted")
	}
	secret := run(Request{Action: "fill", Selector: "#credential-search", Text: "secret-search-value"})
	secretRaw, _ := json.Marshal(secret)
	if strings.Contains(string(secretRaw), "secret-search-value") || !strings.Contains(string(secretRaw), "[redacted]") {
		t.Fatal("credential labelled search leaked")
	}
	run(Request{Action: "evaluate", Expression: `const s=document.querySelector('#stories');s.prepend(s.querySelectorAll('a')[250]);null`})
	shifted := run(Request{Action: "wait", Selector: "#filter", Condition: "visible"})
	if shifted.Snapshot.Mode != "full" || shifted.Change == nil || !shifted.Change.Observed {
		t.Fatal("range shift misreported", shifted.Snapshot)
	}
	// Text budget is global across roots. Equal-sized edits inside a stable
	// truncated prefix can be delta; replacing its cut-point node must be full,
	// even if its visible text is identical.
	run(Request{Action: "evaluate", Expression: `const p=document.createElement('p');p.id='padding';p.textContent='x'.repeat(13000);document.querySelector('#status').after(p);const h=document.createElement('div');document.body.append(h);h.attachShadow({mode:'open'}).innerHTML='<p>Beyond text boundary</p>';null`})
	textFull := run(Request{Action: "snapshot"})
	if !textFull.Snapshot.Omitted || len(textFull.Snapshot.Text) > MaxSnapshotText || strings.Contains(textFull.Snapshot.Text, "Beyond text boundary") {
		t.Fatal("text budget restarted in another root")
	}
	textDelta := run(Request{Action: "click", Selector: "#filter"})
	if textDelta.Snapshot.Mode != "delta" || !strings.Contains(textDelta.Snapshot.Text, "Minimum: 0; median: 5.05") {
		t.Fatal("stable truncated text prefix forced full")
	}
	run(Request{Action: "evaluate", Expression: `const p=document.querySelector('#padding');p.replaceChildren(document.createTextNode(p.textContent));null`})
	boundary := run(Request{Action: "wait", Selector: "#filter", Condition: "visible"})
	if boundary.Snapshot.Mode != "full" || !boundary.Change.Observed {
		t.Fatal("identical-text cut-point replacement misreported as unchanged")
	}
	// Upgrade/reset internals are covered by script tests: arbitrary evaluate
	// must not gain access to the fixed helper's private observation world.
}

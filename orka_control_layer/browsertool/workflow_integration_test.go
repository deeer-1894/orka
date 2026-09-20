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

// Real DOM and input events through the isolated bridge, no provider calls.
func TestRealBrowserWorkflow(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("set ORKA_BROWSER_TEST_WS for isolated Chromium")
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/frame" {
			fmt.Fprint(w, `<!doctype html><label>Frame field<input id="inner"></label><button id="inside" onclick="document.querySelector('#out').textContent='Frame clicked'">Frame action</button><p id="out">Frame ready</p>`)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Browser workflow</title><h1>Browser workflow</h1><label>Query<input id="query"></label><label>Minimum<input type="number" id="minimum"></label><label>Order<select id="order"><option value="asc">Ascending</option><option value="desc">Descending</option></select></label><p id="status">Records 89; median 4.8</p><button id="noop">No change</button><button id="replace" onclick="document.querySelector('#query').outerHTML='<input id=query aria-label=Query>'">Replace</button><button id="rename" onclick="document.querySelector('#noop').textContent='Delete records'">Rename</button><div id="late"></div><input id="readonly" readonly><input id="private"><p id="echo"></p><button class="duplicate">Same</button><button class="duplicate">Same</button><iframe id="same" src="/frame"></iframe><iframe id="cross" sandbox src="/frame" title="Sign in"></iframe><canvas></canvas><script>
document.querySelector('#minimum').oninput=e=>document.querySelector('#status').textContent=e.target.value==='5'?'Records 2; median 5.05':'Records 89; median 4.8';
document.querySelector('#private').oninput=e=>document.querySelector('#echo').textContent=e.target.value;
document.querySelector('#query').dataset.fills='0';document.querySelector('#query').oninput=e=>e.target.dataset.fills=String(Number(e.target.dataset.fills)+1);
</script>`)
	}))
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture.Listener = listener
	fixture.Start()
	defer fixture.Close()
	url := "http://host.docker.internal:" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	id := connectors.GUIIdentity{OwnerID: "workflow-test", ConversationID: fmt.Sprint(time.Now().UnixNano()), RunID: "workflow"}
	dialer := &snapshotEpochDialer{BrowserDialer: connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN"))}
	engine := NewEngine(dialer)
	run := func(req Request) Result {
		t.Helper()
		result, err := engine.Run(context.Background(), id, req, nil)
		if err != nil {
			t.Fatalf("%s: %+v %v", req.Action, result, err)
		}
		return result
	}
	reject := func(req Request, code string) Result {
		t.Helper()
		result, err := engine.Run(context.Background(), id, req, nil)
		if err == nil || result.Error.Code != code {
			t.Fatalf("expected %s: %+v %v", code, result, err)
		}
		return result
	}
	open := func() Result { return run(Request{Action: "open", URL: url}) }
	ref := func(r Result, name string) string {
		t.Helper()
		for _, e := range r.Snapshot.Elements {
			if e.Name == name {
				return e.Ref
			}
		}
		t.Fatalf("missing %s", name)
		return ""
	}
	str := func(s string) *string { return &s }
	t.Run("stable refs and visible differences", func(t *testing.T) {
		first := open()
		q := ref(first, "Query")
		m := ref(first, "Minimum")
		run(Request{Action: "fill", Ref: q, SnapshotID: first.Snapshot.ID, Text: "Japan"})
		changed := run(Request{Action: "fill", Ref: m, SnapshotID: first.Snapshot.ID, Text: "5"})
		if changed.Snapshot.ID != first.Snapshot.ID || changed.Snapshot.Mode != "delta" || !strings.Contains(changed.Snapshot.Text, "5.05") || changed.Change == nil || !changed.Change.Observed {
			t.Fatalf("bad delta: %+v", changed)
		}
		if strings.Contains(changed.Snapshot.Text, "Browser workflow") {
			t.Fatal("repeated full text returned")
		}
		full := run(Request{Action: "snapshot"})
		if full.Snapshot.Mode != "full" || !strings.Contains(full.Snapshot.Text, "Browser workflow") {
			t.Fatal("full observation unavailable")
		}
	})
	t.Run("replaced and renamed targets stay stale", func(t *testing.T) {
		first := open()
		q := ref(first, "Query")
		n := ref(first, "No change")
		run(Request{Action: "click", Selector: "#replace"})
		reject(Request{Action: "fill", Ref: q, SnapshotID: first.Snapshot.ID, Text: "x"}, "stale_ref")
		run(Request{Action: "click", Selector: "#rename"})
		reject(Request{Action: "click", Ref: n, SnapshotID: first.Snapshot.ID}, "stale_ref")
	})
	t.Run("form receipt and no replay on partial failure", func(t *testing.T) {
		first := open()
		q := ref(first, "Query")
		m := ref(first, "Minimum")
		ok := run(Request{Action: "fill_form", Fields: []FormField{{Ref: q, SnapshotID: first.Snapshot.ID, Text: str("Japan")}, {Ref: m, SnapshotID: first.Snapshot.ID, Text: str("5")}, {Selector: "#order", Value: str("desc")}}})
		if ok.Form == nil || ok.Form.Completed != 3 || !strings.Contains(ok.Snapshot.Text, "5.05") {
			t.Fatal("form not applied", ok)
		}
		partial := reject(Request{Action: "fill_form", Fields: []FormField{{Selector: "#query", Text: str("second")}, {Selector: "#readonly", Text: str("never")}, {Selector: "#minimum", Text: str("0")}}}, "not_interactable")
		if partial.Form.Completed != 1 || partial.Form.FailedIndex == nil || *partial.Form.FailedIndex != 1 {
			t.Fatal("missing partial result", partial)
		}
		value := run(Request{Action: "evaluate", Expression: "({fills:Number(document.querySelector('#query').dataset.fills),minimum:document.querySelector('#minimum').value})"})
		raw, _ := json.Marshal(value.Value)
		if string(raw) != `{"fills":2,"minimum":"5"}` {
			t.Fatal("replayed or continued failed form", string(raw))
		}
	})
	t.Run("delayed target and blocked target", func(t *testing.T) {
		open()
		run(Request{Action: "evaluate", Expression: `setTimeout(()=>document.querySelector('#late').innerHTML='<button id=delayed onclick="this.textContent=\'Clicked once\'">Delayed</button>',300);null`})
		r := run(Request{Action: "click", Selector: "#delayed"})
		if !strings.Contains(r.Snapshot.Text, "Clicked once") {
			t.Fatal("delayed target not clicked")
		}
		run(Request{Action: "evaluate", Expression: `document.querySelector('#late').insertAdjacentHTML('beforeend','<div id=cover style="position:fixed;inset:0;z-index:9999;background:white">Blocked</div>');null`})
		reject(Request{Action: "click", Selector: "#noop", TimeoutMS: 200}, "timeout")
		run(Request{Action: "evaluate", Expression: "document.querySelector('#cover').remove();null"})
		run(Request{Action: "click", Selector: "#noop"})
		reject(Request{Action: "click", Selector: ".duplicate"}, "ambiguous_selector")
	})
	t.Run("same-origin frame and explicit GUI boundary", func(t *testing.T) {
		r := open()
		run(Request{Action: "wait", Frame: []string{"#same"}, Selector: "#inside", Condition: "visible"})
		r = run(Request{Action: "snapshot"})
		if len(r.Snapshot.Frames) != 2 || !r.Snapshot.Frames[0].Supported || r.Snapshot.Frames[1].Supported || r.Snapshot.Frames[1].Handoff != "gui" {
			t.Fatal("frame capabilities", r.Snapshot.Frames)
		}
		inner := ref(r, "Frame field")
		run(Request{Action: "fill", Ref: inner, SnapshotID: r.Snapshot.ID, Text: "private frame input"})
		clicked := run(Request{Action: "click", Frame: []string{"#same"}, Selector: "#inside"})
		if !strings.Contains(clicked.Snapshot.Text, "Frame clicked") {
			t.Fatal("frame coordinates wrong", clicked)
		}
		reject(Request{Action: "click", Frame: []string{"#cross"}, Selector: "#inside"}, "unsupported_frame")
		run(Request{Action: "evaluate", Expression: "document.querySelector('#same').src='/frame?next';null"})
		run(Request{Action: "wait", Frame: []string{"#same"}, Selector: "#inside", Condition: "visible"})
		reject(Request{Action: "fill", Ref: inner, SnapshotID: r.Snapshot.ID, Text: "x"}, "stale_ref")
	})
	t.Run("no progress and privacy across delta and handoff", func(t *testing.T) {
		open()
		run(Request{Action: "click", Selector: "#noop"})
		r := run(Request{Action: "click", Selector: "#noop"})
		if r.Progress == nil || r.Progress.UnchangedActions != 2 || r.Snapshot.Mode != "unchanged" {
			t.Fatal("missing progress evidence", r)
		}
		r = run(Request{Action: "fill", Selector: "#private", Text: "secret with spaces"})
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "secret with spaces") {
			t.Fatal("private echo in delta")
		}
		old := run(Request{Action: "snapshot"})
		q := ref(old, "Query")
		dialer.epochOffset++
		reject(Request{Action: "fill", Ref: q, SnapshotID: old.Snapshot.ID, Text: "x"}, "stale_ref")
		r = run(Request{Action: "snapshot"})
		raw, _ = json.Marshal(r)
		if r.Snapshot.Mode != "full" || strings.Contains(string(raw), "secret with spaces") {
			t.Fatal("handoff state/privacy")
		}
	})
}

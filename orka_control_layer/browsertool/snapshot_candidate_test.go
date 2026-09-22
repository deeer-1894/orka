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

// Exercise candidate/commit across real leases without depending on the engine's
// retry timings. A discarded candidate models a failed post-snapshot stability
// check: the next tool call must still report everything since the last commit.
func TestRealSnapshotCandidateCommit(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("set ORKA_BROWSER_TEST_WS for isolated Chromium")
	}
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Candidate fixture</title><input type="checkbox" id="filter" aria-label="Filter" onclick="document.body.dataset.clicks=String(Number(document.body.dataset.clicks||0)+1);document.querySelector('#status').textContent='Status: completed'"><p id="status">Status: pending</p>`)
		for i := 0; i < 260; i++ {
			fmt.Fprintf(w, `<a href="#%d">Story %03d: browser observation</a><br>`, i, i)
		}
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
	who := connectors.GUIIdentity{OwnerID: "snapshot-candidate@example.test", ConversationID: fmt.Sprintf("candidate-%d", time.Now().UnixNano()), RunID: "candidate-run"}
	dialer := connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN"))
	withLease := func(open bool, fn func(context.Context, Session)) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		lease, err := dialer.Acquire(ctx, who, 5*time.Second, 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		}()
		if open {
			if err := lease.Execute(ctx, "Page.navigate", map[string]any{"url": target}, nil); err != nil {
				t.Fatal(err)
			}
		}
		world, err := createWorld(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
		s := Session{Lease: lease, Identity: who, ContextID: world}
		if err := waitFor(ctx, s, Request{Condition: "visible", Selector: "#filter"}); err != nil {
			t.Fatal(err)
		}
		fn(ctx, s)
	}
	read := func(ctx context.Context, s Session, action string, commit bool) pageReply {
		t.Helper()
		r, err := page(ctx, s, "snapshot", Request{Action: action, Selector: "#filter", observation: observationProfiles[ObservationDetailed]})
		if err != nil || r.Snapshot == nil {
			t.Fatalf("candidate: %+v %v", r, err)
		}
		if commit {
			if _, err := page(ctx, s, "commit_observation", Request{SnapshotID: r.Snapshot.ID}); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	var full pageReply
	withLease(true, func(ctx context.Context, s Session) {
		full = read(ctx, s, "snapshot", true)
		if !full.Snapshot.Omitted || len(full.Snapshot.Elements) != MaxSnapshotRefs {
			t.Fatal("fixture did not reach the 200-ref bound")
		}
	})
	withLease(false, func(ctx context.Context, s Session) {
		if err := act(ctx, s, Request{Action: "click", Selector: "#filter"}); err != nil {
			t.Fatal(err)
		}
		if err := waitFor(ctx, s, Request{Condition: "text", Selector: "#status", Text: "Status: completed"}); err != nil {
			t.Fatal(err)
		}
		r := read(ctx, s, "click", false)
		if r.Snapshot.Mode != "delta" {
			t.Fatalf("small filter returned %s", r.Snapshot.Mode)
		}
		// Reject an acknowledgement for another document without advancing it.
		if _, err := page(ctx, s, "commit_observation", Request{SnapshotID: "wrong-document"}); err == nil {
			t.Fatal("unrelated document committed candidate")
		}
	})
	for attempt := 0; attempt < 3; attempt++ {
		withLease(false, func(ctx context.Context, s Session) {
			r := read(ctx, s, "wait", attempt == 2)
			if r.Snapshot.Mode != "delta" || !r.Change.Observed || !strings.Contains(r.Snapshot.Text, "Status: completed") || !strings.Contains(strings.Join(r.Change.Removed, "\n"), "Status: pending") {
				t.Fatalf("discarded candidate hid the transition: mode=%s text=%q change=%+v", r.Snapshot.Mode, r.Snapshot.Text, r.Change)
			}
			if len(r.Snapshot.Elements) != 1 || !r.Snapshot.Elements[0].Checked {
				t.Fatal("current filter state missing")
			}
			if attempt == 2 {
				before, _ := json.Marshal(full)
				after, _ := json.Marshal(r)
				if len(after)*4 >= len(before) {
					t.Fatalf("lost bounded delta: %d -> %d bytes", len(before), len(after))
				}
				t.Logf("260 links, after discarded candidates: %d -> %d bytes (%.1f%% smaller)", len(before), len(after), 100*(1-float64(len(after))/float64(len(before))))
			}
		})
	}
	withLease(false, func(ctx context.Context, s Session) {
		if r := read(ctx, s, "wait", true); r.Snapshot.Mode != "unchanged" {
			t.Fatalf("accepted baseline was not committed: %s", r.Snapshot.Mode)
		}
		clicks, err := evaluate(ctx, s, "Number(document.body.dataset.clicks)")
		if err != nil || clicks != float64(1) {
			t.Fatalf("action replayed: clicks=%v error=%v", clicks, err)
		}
	})
}

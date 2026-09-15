package browsertool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/pathsafe"
)

// Uses only disposable identities and the isolated test bridge. HTTP_HOST lets
// container Chromium reach the host fixture; no production configuration changes.
func TestRealBrowserFilesMaximumDownload(t *testing.T) {
	endpoint := os.Getenv("ORKA_BROWSER_TEST_WS")
	if endpoint == "" {
		t.Skip("requires isolated test bridge")
	}
	payload := bytes.Repeat([]byte{0, 17, 128, 255}, 4<<20)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/csp" {
			if r.URL.Path == "/csp" {
				w.Header().Set("Content-Security-Policy", "connect-src 'none'")
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<title>File fixture</title><p>Binary download fixture</p>")
			return
		}

		if r.URL.Path == "/private" {
			cookie, err := r.Cookie("file_fixture_auth")
			if err != nil || cookie.Value != "fake-only" {
				http.Error(w, "unauthorized", 401)
				return
			}
			w.Header().Set("Content-Type", "text/csv")
			fmt.Fprint(w, "name,value\nfixture,42\n")
			return
		}
		if r.URL.Path == "/stream-overflow" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.(http.Flusher).Flush() // No Content-Length: the streaming limit must enforce the bound.
			_, _ = w.Write(payload)
			_, _ = w.Write([]byte("x"))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	if os.Getenv("ORKA_BROWSER_TEST_HTTP_HOST") != "" {
		_ = server.Listener.Close()
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		server.Listener = listener
	}
	server.Start()
	defer server.Close()
	origin := server.URL
	if host := os.Getenv("ORKA_BROWSER_TEST_HTTP_HOST"); host != "" {
		_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
		origin = "http://" + net.JoinHostPort(host, port)
	}
	base := t.TempDir()
	id := fileFixtureIdentity()
	id.ConversationID = fmt.Sprintf("files-limit-%d", time.Now().UnixNano())
	dialer := &fileCountingDialer{BrowserDialer: connectors.NewBrowserDialer(endpoint, os.Getenv("ORKA_BROWSER_TEST_TOKEN"))}
	engine := NewEngine(dialer)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	opened, err := engine.Run(ctx, id, Request{Action: "open", URL: origin}, NewFiles(base, id))
	if err != nil || !opened.OK {
		t.Fatalf("fixture open: %+v %v", opened, err)
	}
	dialer.commands.Store(0)
	result, err := engine.Run(ctx, id, Request{Action: "download", URL: origin + "/binary", Path: "exports/exact16m.bin", TimeoutMS: 60000}, NewFiles(base, id))
	if err != nil || !result.OK || len(result.Files) != 1 {
		t.Fatalf("maximum download: %+v %v", result, err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(payload))
	file := result.Files[0]
	if file.Size != 16<<20 || file.SHA256 != want || file.MIME != "application/octet-stream" {
		t.Fatalf("bad receipt %+v", file)
	}
	root, _ := pathsafe.SessionRoot(base, id.OwnerID, id.ConversationID)
	data, err := os.ReadFile(filepath.Join(root, file.Path))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("published bytes differ", err)
	}
	if n := dialer.commands.Load(); n > 256 {
		t.Fatalf("lease command budget exceeded: %d", n)
	}
	if n := dialer.reads.Load(); n != 205 {
		t.Fatalf("16 MiB should take 205 raw chunks, got %d", n)
	}
	t.Logf("real Chromium 16 MiB SHA256=%s, data_chunks=%d, total_cdp_commands=%d", want, dialer.reads.Load(), dialer.commands.Load())

	t.Run("authenticated_and_blob_downloads", func(t *testing.T) {
		run := func(req Request) Result {
			t.Helper()
			r, err := engine.Run(ctx, id, req, NewFiles(base, id))
			if err != nil || !r.OK {
				t.Fatalf("file fixture action %s: %+v %v", req.Action, r, err)
			}
			return r
		}
		denied, err := engine.Run(ctx, id, Request{Action: "download", URL: origin + "/private", Path: "exports/denied.csv"}, NewFiles(base, id))
		if err == nil || denied.OK {
			t.Fatal("unauthenticated endpoint accepted")
		}
		run(Request{Action: "evaluate", Expression: "document.cookie='file_fixture_auth=fake-only; path=/'; true"})
		receipt := run(Request{Action: "download", URL: origin + "/private", Path: "exports/private.csv"})
		b, err := os.ReadFile(filepath.Join(root, receipt.Files[0].Path))
		if err != nil || string(b) != "name,value\nfixture,42\n" {
			t.Fatal("authenticated download bytes wrong", err)
		}
		exported := run(Request{Action: "evaluate", Expression: "URL.createObjectURL(new Blob(['blob fixture bytes'],{type:'text/plain'}))"})
		blobURL, ok := exported.Value.(string)
		if !ok {
			t.Fatalf("blob URL missing: %T", exported.Value)
		}
		receipt = run(Request{Action: "download", URL: blobURL, Path: "exports/blob.txt"})
		b, err = os.ReadFile(filepath.Join(root, receipt.Files[0].Path))
		if err != nil || string(b) != "blob fixture bytes" {
			t.Fatal("blob bytes wrong", err)
		}
	})
	t.Run("streaming_overflow_leaves_no_file", func(t *testing.T) {
		result, err := engine.Run(ctx, id, Request{Action: "download", URL: origin + "/stream-overflow", Path: "exports/overflow.bin"}, NewFiles(base, id))
		if err == nil || result.OK || result.Error == nil || result.Error.Code != "output_limit" {
			t.Fatalf("stream overlimit: %+v %v", result, err)
		}
		if _, err := os.Stat(filepath.Join(root, "exports/overflow.bin")); !os.IsNotExist(err) {
			t.Fatal("overflow published file")
		}
	})
	t.Run("page_CSP_blocks_fetch", func(t *testing.T) {
		opened, err := engine.Run(ctx, id, Request{Action: "open", URL: origin + "/csp"}, NewFiles(base, id))
		if err != nil || !opened.OK {
			t.Fatalf("CSP page open: %+v %v", opened, err)
		}
		result, err := engine.Run(ctx, id, Request{Action: "download", URL: origin + "/binary", Path: "exports/csp.bin"}, NewFiles(base, id))
		if err == nil || result.OK {
			t.Fatal("page connect-src none was bypassed by download")
		}
		if _, err := os.Stat(filepath.Join(root, "exports/csp.bin")); !os.IsNotExist(err) {
			t.Fatal("CSP failure published file")
		}
	})

}

type fileCountingDialer struct {
	connectors.BrowserDialer
	commands atomic.Int64
	reads    atomic.Int64
}

func (d *fileCountingDialer) Acquire(ctx context.Context, id connectors.GUIIdentity, q, e time.Duration) (connectors.BrowserLease, error) {
	l, err := d.BrowserDialer.Acquire(ctx, id, q, e)
	if err != nil {
		return nil, err
	}
	return &fileCountingLease{BrowserLease: l, d: d}, nil
}

type fileCountingLease struct {
	connectors.BrowserLease
	d *fileCountingDialer
}

func (l *fileCountingLease) Execute(ctx context.Context, m string, p, out any) error {
	l.d.commands.Add(1)
	if m == "Runtime.callFunctionOn" {
		if v, ok := p.(map[string]any); ok {
			if f, ok := v["functionDeclaration"].(string); ok && strings.Contains(f, "orkaDownloadRead") {
				l.d.reads.Add(1)
			}
		}
	}
	return l.BrowserLease.Execute(ctx, m, p, out)
}

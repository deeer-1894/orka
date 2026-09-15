package browsertool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/delivery"
	"github.com/orka-oss/orka_core/pathsafe"
)

type fileFixtureLease struct {
	call   func(context.Context, string, any, any) error
	closed bool
}

func (f *fileFixtureLease) Execute(ctx context.Context, m string, p, out any) error {
	return f.call(ctx, m, p, out)
}
func (f *fileFixtureLease) Info() connectors.BrowserPageInfo {
	return connectors.BrowserPageInfo{LeaseID: "fixture-lease", PageID: "fixture-page", PageEpoch: 1}
}
func (f *fileFixtureLease) Close() error { f.closed = true; return nil }
func fileFixtureIdentity() connectors.GUIIdentity {
	return connectors.GUIIdentity{OwnerID: "browser-fixture@example.test", ConversationID: "one", RunID: "run-one"}
}
func fileFixtureJSON(t *testing.T, out, value any) error {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return json.Unmarshal(b, out)
}
func fileFixturePNG(t *testing.T) []byte {
	t.Helper()
	pic := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pic.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, pic); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestFilesScreenshotPublishesValidatedBytesToIdentityWorkspace(t *testing.T) {
	base := t.TempDir()
	id := fileFixtureIdentity()
	expected := fileFixturePNG(t)
	lease := &fileFixtureLease{call: func(ctx context.Context, m string, p, out any) error {
		if m != "Page.captureScreenshot" {
			t.Fatalf("unexpected operation %s", m)
		}
		return fileFixtureJSON(t, out, map[string]any{"data": base64.StdEncoding.EncodeToString(expected)})
	}}
	files, err := NewFiles(base, id).Execute(context.Background(), Session{Lease: lease, Identity: id, ContextID: 7}, Request{Action: "screenshot", Path: "images/page.png"})
	if err != nil || len(files) != 1 {
		t.Fatalf("screenshot: %+v %v", files, err)
	}
	if files[0].Path != "images/page.png" || files[0].MIME != "image/png" || files[0].Size != int64(len(expected)) || len(files[0].SHA256) != 64 {
		t.Fatalf("receipt=%+v", files)
	}
	root, err := pathsafe.SessionRoot(base, id.OwnerID, id.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, files[0].Path))
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("file=%v %v", actual, err)
	}
	if lease.closed {
		t.Fatal("file action closed engine-owned lease")
	}
}

func TestFilesDownloadUsesScopedStreamAndReturnsMetadataOnly(t *testing.T) {
	base := t.TempDir()
	id := fileFixtureIdentity()
	closed, released := false, false
	lease := &fileFixtureLease{call: func(ctx context.Context, m string, p, out any) error {
		params := p.(map[string]any)
		switch m {
		case "Runtime.evaluate":
			if params["contextId"] != int64(7) || params["returnByValue"] != false || params["awaitPromise"] != true {
				t.Fatalf("unscoped download evaluation: %v", params)
			}
			return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"type": "object", "objectId": "download-object"}})
		case "Runtime.callFunctionOn":
			if params["objectId"] != "download-object" {
				t.Fatal("wrong stream object")
			}
			if strings.Contains(params["functionDeclaration"].(string), "orkaDownloadClose") {
				closed = true
				return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"value": true}})
			}
			return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"value": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("name,value\nA,42\n")), "done": true, "mime": "text/csv; charset=utf-8"}}})
		case "Runtime.releaseObject":
			released = true
			return nil
		default:
			t.Fatalf("unexpected operation %s", m)
		}
		return nil
	}}
	files, err := NewFiles(base, id).Execute(context.Background(), Session{Lease: lease, Identity: id, ContextID: 7}, Request{Action: "download", URL: "https://fixture.test/private.csv", Path: "data/report.csv"})
	if err != nil || len(files) != 1 {
		t.Fatalf("download=%+v %v", files, err)
	}
	if files[0].MIME != "text/csv" || files[0].Size != 16 || !closed || !released || lease.closed {
		t.Fatalf("download receipt/cleanup=%+v %t %t", files, closed, released)
	}
	b, _ := json.Marshal(files)
	if strings.Contains(string(b), "bmFtZS") || strings.Contains(string(b), "name,value") {
		t.Fatal("model result contains downloaded bytes")
	}
}

func TestFilesRejectsOversizedRawChunkDespiteBase64Padding(t *testing.T) {
	base := t.TempDir()
	id := fileFixtureIdentity()
	released := false
	lease := &fileFixtureLease{call: func(ctx context.Context, method string, p, out any) error {
		switch method {
		case "Runtime.evaluate":
			return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"objectId": "stream"}})
		case "Runtime.callFunctionOn":
			if strings.Contains(p.(map[string]any)["functionDeclaration"].(string), "orkaDownloadClose") {
				return nil
			}
			return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"value": map[string]any{"data": base64.StdEncoding.EncodeToString(make([]byte, 81921)), "done": true}}})
		case "Runtime.releaseObject":
			released = true
		}
		return nil
	}}
	files, err := NewFiles(base, id).Execute(context.Background(), Session{Lease: lease, Identity: id, ContextID: 7}, Request{Action: "download", URL: "https://fixture.test/data", Path: "data.bin"})
	if err == nil || len(files) != 0 {
		t.Fatalf("overlarge raw chunk published: %v %v", files, err)
	}
	if !released {
		t.Fatal("stream leaked")
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 0 {
		t.Fatal("failed download created workspace")
	}
}

func TestFilesRejectsIdentityAndUnsafePathsBeforeBrowserAccess(t *testing.T) {
	base := t.TempDir()
	id := fileFixtureIdentity()
	lease := &fileFixtureLease{call: func(context.Context, string, any, any) error { t.Fatal("invalid request reached browser"); return nil }}
	cases := []struct {
		name       string
		id         connectors.GUIIdentity
		path, mode string
	}{
		{"traversal", id, "../other/file.png", ""}, {"absolute", id, "/etc/file.png", ""}, {"append", id, "file.png", "append"},
	}
	other := id
	other.ConversationID = "two"
	cases = append(cases, struct {
		name       string
		id         connectors.GUIIdentity
		path, mode string
	}{"cross-session", other, "file.png", ""})
	other = id
	other.OwnerID = "other@example.test"
	cases = append(cases, struct {
		name       string
		id         connectors.GUIIdentity
		path, mode string
	}{"cross-owner", other, "file.png", ""})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, err := NewFiles(base, id).Execute(context.Background(), Session{Lease: lease, Identity: tc.id}, Request{Action: "screenshot", Path: tc.path, Mode: tc.mode})
			if err == nil || len(files) != 0 {
				t.Fatal("unsafe publication accepted")
			}
		})
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 0 {
		t.Fatal("invalid identity/path created workspace")
	}
}

func TestFilesInvalidScreenshotNeverPublishes(t *testing.T) {
	for _, kind := range []string{"encoding", "not_png", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			id := fileFixtureIdentity()
			data := "!not-base64!"
			if kind == "not_png" {
				data = base64.StdEncoding.EncodeToString([]byte("not a PNG"))
			}
			if kind == "truncated" {
				png := fileFixturePNG(t)
				data = base64.StdEncoding.EncodeToString(png[:len(png)-8])
			}
			lease := &fileFixtureLease{call: func(ctx context.Context, m string, p, out any) error {
				return fileFixtureJSON(t, out, map[string]any{"data": data})
			}}
			files, err := NewFiles(base, id).Execute(context.Background(), Session{Lease: lease, Identity: id}, Request{Action: "screenshot", Path: "evidence.png"})
			if err == nil || len(files) != 0 {
				t.Fatal("invalid image published")
			}
			entries, _ := os.ReadDir(base)
			if len(entries) != 0 {
				t.Fatal("invalid image created workspace")
			}
		})
	}
}

func TestFilesDownloadFailuresCleanupAndLeaveNoOutput(t *testing.T) {
	for _, kind := range []string{"http", "cors", "overflow", "cancel", "no_progress"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			id := fileFixtureIdentity()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			released := false
			closed := false
			lease := &fileFixtureLease{call: func(callctx context.Context, m string, p, out any) error {
				switch m {
				case "Runtime.evaluate":
					if kind == "http" || kind == "cors" {
						detail := "TypeError fetch failed https://fixture.test/?private=never-print"
						if kind == "http" {
							detail = "ORKA_HTTP_403"
						}
						return fileFixtureJSON(t, out, map[string]any{"exceptionDetails": map[string]any{"text": detail}})
					}
					return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"objectId": "stream"}})
				case "Runtime.callFunctionOn":
					if strings.Contains(p.(map[string]any)["functionDeclaration"].(string), "orkaDownloadClose") {
						closed = true
						if callctx.Err() != nil {
							t.Fatal("cleanup inherited cancellation")
						}
						return nil
					}
					if kind == "cancel" {
						cancel()
						return context.Canceled
					}
					if kind == "overflow" {
						return fileFixtureJSON(t, out, map[string]any{"exceptionDetails": map[string]any{"text": "ORKA_OUTPUT_LIMIT"}})
					}
					return fileFixtureJSON(t, out, map[string]any{"result": map[string]any{"value": map[string]any{"data": "", "done": false}}})
				case "Runtime.releaseObject":
					released = true
				}
				return nil
			}}
			files, err := NewFiles(base, id).Execute(ctx, Session{Lease: lease, Identity: id, ContextID: 7}, Request{Action: "download", URL: "https://fixture.test/download", Path: "file.bin"})
			if err == nil || len(files) != 0 {
				t.Fatal("failure returned a file")
			}
			if strings.Contains(err.Error(), "never-print") {
				t.Fatal("browser exception secret leaked")
			}
			if kind != "http" && kind != "cors" && (!closed || !released) {
				t.Fatal("stream not cleaned up")
			}
			entries, _ := os.ReadDir(base)
			if len(entries) != 0 {
				t.Fatal("download failure created output")
			}
		})
	}
}

func TestFilesCreateReplaceHistoryAndFixedDelivery(t *testing.T) {
	base := t.TempDir()
	id := fileFixtureIdentity()
	first := fileFixturePNG(t)
	data := first
	lease := &fileFixtureLease{call: func(ctx context.Context, m string, p, out any) error {
		return fileFixtureJSON(t, out, map[string]any{"data": base64.StdEncoding.EncodeToString(data)})
	}}
	execute := func(mode string) ([]FileResult, error) {
		return NewFiles(base, id).Execute(context.Background(), Session{Lease: lease, Identity: id}, Request{Action: "screenshot", Path: "output.png", Mode: mode})
	}
	if _, err := execute(""); err != nil {
		t.Fatal(err)
	}
	if _, err := delivery.Publish(base, id.OwnerID, id.ConversationID, id.RunID, []string{"output.png"}); err != nil {
		t.Fatal(err)
	}
	// A second valid image changes pixels and encoded bytes.
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	data = encoded.Bytes()
	if files, err := execute(""); err == nil || len(files) != 0 {
		t.Fatal("create overwrote existing file")
	}
	if _, err := execute("replace"); err != nil {
		t.Fatal(err)
	}
	root, _ := pathsafe.SessionRoot(base, id.OwnerID, id.ConversationID)
	current, err := os.ReadFile(filepath.Join(root, "output.png"))
	if err != nil || !bytes.Equal(current, data) {
		t.Fatal("replacement missing", err)
	}
	backups, err := filepath.Glob(filepath.Join(root, ".orka_trash", "*", "output.png"))
	if err != nil || len(backups) != 1 {
		t.Fatal("previous version not saved", err, backups)
	}
	prior, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(prior, first) {
		t.Fatal("version bytes changed", err)
	}
	frozen, err := delivery.Read(base, id.OwnerID, id.ConversationID, id.RunID, "output.png")
	if err != nil || !bytes.Equal(frozen, first) {
		t.Fatal("live replacement changed fixed delivery", err)
	}
}

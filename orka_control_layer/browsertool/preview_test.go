package browsertool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/orka-oss/orka_core/pathsafe"
)

func TestPreviewOpensCurrentSessionHTMLWithoutReturningDocument(t *testing.T) {
	base := t.TempDir()
	who := testIdentity()
	root, err := pathsafe.EnsureSession(base, who.OwnerID, who.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("<!doctype html><h1>Actual report</h1><script>const secretPayload='" + strings.Repeat("x", 150000) + "';</script>")
	if err = os.WriteFile(filepath.Join(root, "report.html"), body, 0600); err != nil {
		t.Fatal(err)
	}
	var transferred []byte
	lease := &fixtureLease{handler: func(method string, params, out any) (bool, error) {
		if method != "Orka.previewHTML" {
			return false, nil
		}
		transferred, err = base64.StdEncoding.DecodeString(params.(map[string]any)["html_base64"].(string))
		return true, err
	}}
	result, err := NewEngine(&fixtureDialer{lease: lease}).Run(context.Background(), who, Request{Action: "preview", Path: "report.html"}, NewFiles(base, who))
	if err != nil || !result.OK || string(transferred) != string(body) || result.Snapshot == nil {
		t.Fatalf("preview failed: %+v %v", result, err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secretPayload") || !strings.Contains(string(raw), "sha256") {
		t.Fatal("missing receipt or document leaked into model result")
	}
}

func TestPreviewFileBoundary(t *testing.T) {
	base := t.TempDir()
	who := testIdentity()
	root, err := pathsafe.EnsureSession(base, who.OwnerID, who.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewFiles(base, who).(PreviewReader)
	for name, body := range map[string][]byte{"empty.html": {}, "binary.html": {255}, "too-large.html": []byte(strings.Repeat("x", maxPreviewBytes+1)), "valid.html": []byte("<h1>Owner document</h1>")} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.html")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.html")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.html"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty.html", "binary.html", "too-large.html", "link.html", "pipe.html", "../secret.html", "missing.html"} {
		if body, _, err := reader.ReadPreview(context.Background(), who, name); err == nil || len(body) != 0 {
			t.Fatalf("read forbidden input %s: %v", name, err)
		}
	}
	other := who
	other.ConversationID = "different"
	if _, _, err := reader.ReadPreview(context.Background(), other, "valid.html"); err == nil {
		t.Fatal("mismatched operation read file")
	}
	if _, _, err := NewFiles(base, other).(PreviewReader).ReadPreview(context.Background(), other, "valid.html"); err == nil {
		t.Fatal("other conversation read file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := reader.ReadPreview(ctx, who, "valid.html"); err == nil {
		t.Fatal("cancelled read succeeded")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), other.ConversationID)); !os.IsNotExist(err) {
		t.Fatal("preview created an absent workspace")
	}
}

func TestPreviewSchemaAllowsOnlyWorkspaceInput(t *testing.T) {
	if _, err := parseRequest(map[string]any{"action": "preview", "path": "report.html"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "preview", "path": "../other/report.html"},
		{"action": "preview", "path": "report.html", "url": "https://example.com"},
		{"action": "preview", "path": "report.html", "mode": "replace"},
		{"action": "preview", "path": "report.html", "html": "unscoped content"},
	} {
		if _, err := parseRequest(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

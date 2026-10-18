package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclaredCheckRejectsUnpublishedEvidence(t *testing.T) {
	root := t.TempDir()
	spec := []byte(`{"kind":"orka.acceptance/v1","requirements":[{"id":"tests","method":"contains","file":"test.log","expected":"OK"}]}`)
	for name, data := range map[string][]byte{"result.acceptance.json": spec, "test.log": []byte("Ran 21 tests\nOK\n")} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if report := Check(context.Background(), root, []string{"result.acceptance.json"}); !report.OK {
		t.Fatal(report)
	}
	report := CheckDeclared(context.Background(), root, []string{"result.acceptance.json"})
	if report.OK || !strings.Contains(strings.Join(report.Failures, " "), "test.log") || !strings.Contains(strings.Join(report.Failures, " "), "update_plan.outputs") {
		t.Fatalf("missing actionable dependency failure: %+v", report)
	}
	if report := CheckDeclared(context.Background(), root, []string{"result.acceptance.json", "test.log"}); !report.OK {
		t.Fatal(report)
	}
}

func TestDeclaredCheckRejectsUnpublishedHTMLResource(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"index.html": "<!doctype html><html><head><link rel=\"stylesheet\" href=\"style.css\"></head><body>Report</body></html>", "style.css": "body { color: black }"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if report := CheckDeclared(context.Background(), root, []string{"index.html"}); report.OK {
		t.Fatal("unpublished stylesheet accepted")
	}
	if report := CheckDeclared(context.Background(), root, []string{"index.html", "style.css"}); !report.OK {
		t.Fatal(report)
	}
}

func TestDeclaredCheckRejectsSymlinkAliases(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "hidden"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hidden", "evidence.log"), []byte("OK"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("hidden/evidence.log", filepath.Join(root, "alias.log")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink("hidden", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alias.log", "alias/evidence.log"} {
		if report := CheckDeclared(context.Background(), root, []string{name}); report.OK {
			t.Fatalf("symlink alias %s accepted", name)
		}
	}
}

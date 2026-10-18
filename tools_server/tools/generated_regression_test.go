package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQRCodeRetainsPreviousVersion(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "writer", "sessions", "test")
	os.MkdirAll(root, 0700)
	os.WriteFile(filepath.Join(root, "code.png"), []byte("previous-image"), 0600)
	r, err := qrGenerate(base)(writeGuardContext(), callWith(map[string]any{"text": "fixture", "path": "code.png"}))
	if err != nil || r.IsError {
		t.Fatalf("qrcode failed: %v %v", r, err)
	}
	versions, _ := filepath.Glob(filepath.Join(root, TrashDir, "*", "code.png"))
	if len(versions) != 1 {
		t.Fatal("generator silently overwrote without a version")
	}
	b, _ := os.ReadFile(versions[0])
	if string(b) != "previous-image" {
		t.Fatal("backup is not original")
	}
}

func TestPythonGeneratorsPublishWithVersion(t *testing.T) {
	t.Setenv("CODE_SANDBOX_MODE", "bwrap")
	if os.Getenv("CODE_BWRAP_PATH") == "" {
		t.Setenv("CODE_SANDBOX_MODE", "unsafe-dev")
	}
	for _, key := range []string{"XL_OUT", "CX_OUT", "SQL_OUT", "J_OUT", "SL_OUT", "CHART_OUT"} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "report.bin"), []byte("old"), 0600)
			script := fmt.Sprintf("import os;open(os.environ[%q], 'wb').write(bytes([0,1,255]))", key)
			out, err := runPython(context.Background(), root, script, []string{key + "=report.bin"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "mode=replace") {
				t.Fatalf("missing publication receipt: %s", out)
			}
			versions, _ := filepath.Glob(filepath.Join(root, TrashDir, "*", "report.bin"))
			if len(versions) != 1 {
				t.Fatal("missing previous version")
			}
			b, _ := os.ReadFile(filepath.Join(root, "report.bin"))
			if len(b) != 3 || b[2] != 255 {
				t.Fatal("binary output changed")
			}
		})
	}
}

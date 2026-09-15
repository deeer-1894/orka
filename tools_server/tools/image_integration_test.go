package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/workspaceio"
	"github.com/orka-oss/tools_server/identity"
	"github.com/orka-oss/tools_server/runner"
	"golang.org/x/text/unicode/norm"
)

// Run the compiled test binary in the target Docker image as its non-root
// workspace UID. An unavailable sandbox is a failure, never a skipped office
// test; all fixtures are synthetic and no gateway credentials are required.
func TestImageOfficeSandbox(t *testing.T) {
	if os.Getenv("ORKA_TOOLS_IMAGE_TEST") != "1" {
		t.Skip("target image integration only")
	}
	t.Setenv("CODE_SANDBOX_MODE", "bwrap")
	if os.Getuid() == 0 {
		t.Fatal("image validation must run non-root")
	}
	base := t.TempDir()
	if storage := os.Getenv("ORKA_IMAGE_STORAGE"); storage != "" {
		var err error
		base, err = os.MkdirTemp(storage, ".image-validation-")
		if err != nil {
			t.Fatalf("non-root workspace mount is not writable: %v", err)
		}
		defer os.RemoveAll(base)
	}
	ctx := identity.With(context.Background(), identity.Identity{Email: "image-fixture@example.test", ConversationID: "one"})
	root, err := pathsafe.EnsureSession(base, "image-fixture@example.test", "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.FromEnv().Run(ctx, runner.Request{Root: root, Program: "true"}); err != nil {
		t.Fatalf("image isolation unavailable: %v", err)
	}
	for name, content := range map[string]string{
		"report.md": "# Image fixture\n\nSandbox office delivery.\n\n## Chart\n\n- synthetic data\n",
		"data.csv":  "name,value\nalpha,1\nbeta,2\n",
		"right.csv": "name,label\nalpha,first\nbeta,second\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	call := func(t *testing.T, handler mcpserver.ToolHandlerFunc, args map[string]any) {
		t.Helper()
		res, err := handler(ctx, callWith(args))
		if err != nil || res == nil || res.IsError {
			t.Fatalf("office failed: %v %v", err, res)
		}
	}
	for _, format := range []string{"html", "docx", "pdf"} {
		t.Run("doc_export_"+format, func(t *testing.T) {
			target := "report." + format
			if err := os.WriteFile(filepath.Join(root, target), []byte("prior-version"), 0600); err != nil {
				t.Fatal(err)
			}
			call(t, docExport(base), map[string]any{"path": "report.md", "to": format, "out": target})
			data, err := os.ReadFile(filepath.Join(root, target))
			if err != nil || len(data) < 20 {
				t.Fatalf("invalid export: %d %v", len(data), err)
			}
			if format == "pdf" && !bytes.HasPrefix(data, []byte("%PDF-")) {
				t.Fatal("not a PDF")
			}
			if format == "docx" {
				if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
					t.Fatal(err)
				}
			}
			stamps, err := os.ReadDir(filepath.Join(root, workspaceio.TrashDir))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, stamp := range stamps {
				old, err := os.ReadFile(filepath.Join(root, workspaceio.TrashDir, stamp.Name(), target))
				if err == nil && string(old) == "prior-version" {
					found = true
				}
			}
			if !found {
				t.Fatal("prior output version missing")
			}

		})
	}
	t.Run("csv_xlsx_roundtrip", func(t *testing.T) {
		call(t, csvToXLSX(base), map[string]any{"path": "data.csv", "out": "roundtrip.xlsx"})
		call(t, xlsxToCSV(base), map[string]any{"path": "roundtrip.xlsx", "out": "roundtrip.csv"})
		data, err := os.ReadFile(filepath.Join(root, "roundtrip.csv"))
		if err != nil || !strings.Contains(string(data), "alpha,1") {
			t.Fatalf("roundtrip: %s %v", data, err)
		}
	})
	t.Run("chart", func(t *testing.T) {
		call(t, chartGenerate(base), map[string]any{"data": "data.csv", "type": "bar", "x": "name", "y": "value", "out": "chart.png"})
		data, err := os.ReadFile(filepath.Join(root, "chart.png"))
		if err != nil || !bytes.HasPrefix(data, []byte("\x89PNG")) {
			t.Fatalf("chart: %v", err)
		}
	})
	t.Run("slides", func(t *testing.T) {
		call(t, slidesGenerate(base), map[string]any{"content": "# Fixture\n\n## Slide\n- synthetic data", "out": "slides.pptx"})
		data, err := os.ReadFile(filepath.Join(root, "slides.pptx"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("sql", func(t *testing.T) {
		call(t, sqlQuery(base), map[string]any{"tables": "data.csv", "query": "select sum(value) as total from data", "out": "sql.csv"})
		data, err := os.ReadFile(filepath.Join(root, "sql.csv"))
		if err != nil || !strings.Contains(string(data), "3") {
			t.Fatalf("sql: %s %v", data, err)
		}
	})
	t.Run("join", func(t *testing.T) {
		call(t, csvJoin(base), map[string]any{"left": "data.csv", "right": "right.csv", "on": "name", "out": "joined.csv"})
		data, err := os.ReadFile(filepath.Join(root, "joined.csv"))
		if err != nil || !strings.Contains(string(data), "first") {
			t.Fatalf("join: %s %v", data, err)
		}
	})
	t.Run("doc_read", func(t *testing.T) { call(t, docRead(base), map[string]any{"path": "report.docx", "out": "read.md"}) })
	t.Run("pdf_extract", func(t *testing.T) {
		call(t, pdfExtract(base), map[string]any{"path": "report.pdf", "out": "extracted.txt"})
		data, err := os.ReadFile(filepath.Join(root, "extracted.txt"))
		if err != nil || !strings.Contains(norm.NFKC.String(string(data)), "Sandbox office delivery") {
			t.Fatalf("pdf text: %s %v", data, err)
		}
	})
}

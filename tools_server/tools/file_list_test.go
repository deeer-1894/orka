package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func listIn(t *testing.T, dir string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "file_list"
	req.Params.Arguments = map[string]any{"path": "."}
	res, err := fileList(dir)(context.Background(), req)
	if err != nil {
		t.Fatalf("file_list: %v", err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// A listing is a lookup, not a payload. Unbounded, one of these was the largest
// single message in an orchestrator's context for 14 of 40 cycles at ~3,900
// tokens — an archive directory that had grown to 303 entries across runs.
func TestFileListCapsLongDirectories(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "_anonymous")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+strconv.Itoa(i)+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := listIn(t, base)

	if n := strings.Count(out, ".txt"); n > maxListEntries {
		t.Errorf("listed %d entries, cap is %d", n, maxListEntries)
	}
	if !strings.Contains(out, "and 140 more entries") {
		t.Errorf("truncation not reported, so the agent cannot tell it saw a subset:\n%s", out)
	}
}

// The offload archive and the overwrite backups are the agent's own plumbing.
// Listing them invites it to browse machinery; reading a known path inside them
// still works, which is all the offload placeholder ever asks for.
func TestFileListHidesInternalDirectories(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "_anonymous")
	for _, d := range []string{offloadDirName, trashDirName, "notes"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out := listIn(t, base)

	for _, hidden := range []string{offloadDirName, trashDirName} {
		if strings.Contains(out, hidden) {
			t.Errorf("%s should not appear in a listing:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, "notes") {
		t.Errorf("a real directory was hidden:\n%s", out)
	}
	if !strings.Contains(out, "2 internal directories hidden") {
		t.Errorf("hiding is not disclosed, which would be a silent omission:\n%s", out)
	}
}

// An ordinary workspace must read exactly as before.
func TestFileListUnchangedForSmallDirectories(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "_anonymous")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := listIn(t, base)

	if !strings.Contains(out, "f\ta.md") || !strings.Contains(out, "d\tsub") {
		t.Errorf("normal listing changed:\n%s", out)
	}
	if strings.Contains(out, "more entries") || strings.Contains(out, "hidden") {
		t.Errorf("clean listing gained noise:\n%s", out)
	}
}

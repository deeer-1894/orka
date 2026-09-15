package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/orka-oss/tools_server/identity"
)

// Replaying a compacted historical call must not overwrite a real artifact or
// create backups/directories. An explicit empty string remains a valid write.
func TestFileWriteRejectsHistoryWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		present bool
	}{
		{"missing", nil, false}, {"null", nil, true}, {"nonstring", 42, true},
		{"old argument", " <persisted-arg>已写入 outputs/observations.md,用 file_read 读取</persisted-arg>\n", true},
		{"output pointer", "<persisted-output>full content archived</persisted-output>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "writer", "sessions", "test")
			file := filepath.Join(root, "notes.md")
			if err := os.MkdirAll(root, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("original evidence"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx := identity.With(context.Background(), identity.Identity{Email: "writer", ConversationID: "test"})
			for _, path := range []string{"notes.md", "new/sub/file.md"} {
				args := map[string]any{"path": path, "mode": "replace", "_orka_history": "omitted content; file_read notes.md"}
				if tc.present {
					args["content"] = tc.value
				}
				result, err := fileWrite(base)(ctx, callWith(args))
				if err != nil {
					t.Fatal(err)
				}
				if !result.IsError {
					t.Error("history replay was accepted")
				}
			}
			got, err := os.ReadFile(file)
			if err != nil || string(got) != "original evidence" {
				t.Errorf("original modified: %q %v", got, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Errorf("rejected write caused filesystem effects: %v %v", entries, err)
			}
		})
	}
}

func TestFileWriteAllowsEmptyAndQuotedMarkers(t *testing.T) {
	for _, content := range []string{"", "Documentation example: <persisted-arg>sample</persisted-arg> ends here.", "ordinary report"} {
		base := t.TempDir()
		ctx := identity.With(context.Background(), identity.Identity{Email: "writer", ConversationID: "test"})
		result, err := fileWrite(base)(ctx, callWith(map[string]any{"path": "notes.md", "content": content}))
		if err != nil || result.IsError {
			t.Fatalf("legitimate write rejected: %v %v", result, err)
		}
		got, err := os.ReadFile(filepath.Join(base, "writer", "sessions", "test", "notes.md"))
		if err != nil || string(got) != content {
			t.Fatalf("content changed: %q %v", got, err)
		}
	}
}

func writeGuardContext() context.Context {
	return identity.With(context.Background(), identity.Identity{Email: "writer", ConversationID: "test"})
}

func writeGuardText(result *mcp.CallToolResult) string {
	var text string
	for _, content := range result.Content {
		if part, ok := content.(mcp.TextContent); ok {
			text += part.Text
		}
	}
	return text
}

func assertWriteGuardFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %s = %q, %v; want %q", path, got, err, want)
	}
}

func TestFileWriteCreateRejectsExistingWithoutBackup(t *testing.T) {
	for _, mode := range []string{"", "create"} {
		for _, original := range []string{"", "original evidence\n第二段\n"} {
			t.Run(fmt.Sprintf("mode=%s/empty=%t", mode, original == ""), func(t *testing.T) {
				base := t.TempDir()
				root := filepath.Join(base, "writer", "sessions", "test")
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "record.txt")
				if err := os.WriteFile(path, []byte(original), 0644); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				args := map[string]any{"path": "record.txt", "content": "supplement"}
				if mode != "" {
					args["mode"] = mode
				}
				result, err := fileWrite(base)(writeGuardContext(), callWith(args))
				if err != nil {
					t.Fatal(err)
				}
				if !result.IsError {
					t.Errorf("existing file accepted: %s", writeGuardText(result))
				}
				for _, hint := range []string{"append", "file_read", "replace", "complete"} {
					if !strings.Contains(writeGuardText(result), hint) {
						t.Errorf("missing recovery hint %q: %s", hint, writeGuardText(result))
					}
				}
				assertWriteGuardFile(t, path, original)
				after, err := os.Stat(path)
				if err != nil || !before.ModTime().Equal(after.ModTime()) {
					t.Errorf("rejected write touched original: %v", err)
				}
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 1 {
					t.Errorf("create rejection created backup or other files: %v %v", entries, err)
				}
			})
		}
	}
}

func TestFileWriteExplicitModesPreserveBackupAndReportBytes(t *testing.T) {
	for _, mode := range []string{"append", "replace"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "writer", "sessions", "test")
			if err := os.MkdirAll(root, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "record.txt")
			original, content := "original\x00bytes\n", "补充\n"
			if err := os.WriteFile(path, []byte(original), 0644); err != nil {
				t.Fatal(err)
			}
			result, err := fileWrite(base)(writeGuardContext(), callWith(map[string]any{"path": "record.txt", "content": content, "mode": mode}))
			if err != nil || result.IsError {
				t.Fatalf("write rejected: %v %v", result, err)
			}
			want := content
			if mode == "append" {
				want = original + content
			}
			assertWriteGuardFile(t, path, want)
			backups, err := filepath.Glob(filepath.Join(root, TrashDir, "*", "record.txt"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("missing backup: %v %v", backups, err)
			}
			assertWriteGuardFile(t, backups[0], original)
			for _, hint := range []string{"mode=" + mode, fmt.Sprintf("%d bytes", len(content)), "previous version saved"} {
				if !strings.Contains(writeGuardText(result), hint) {
					t.Errorf("missing outcome %q: %s", hint, writeGuardText(result))
				}
			}
		})
	}
}

func TestFileWriteInvalidModeBeforeFilesystemOperations(t *testing.T) {
	for _, mode := range []any{nil, 42, true, "", "overwrite", "APPEND", " append "} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			base := t.TempDir()
			// Even an invalid path must not get as far as resolution. No user
			// workspace, parent directories, or version history may be created.
			for _, path := range []string{"../../escape", "new/sub/file.txt"} {
				result, err := fileWrite(base)(writeGuardContext(), callWith(map[string]any{"path": path, "content": "valid", "mode": mode}))
				if err != nil {
					t.Fatal(err)
				}
				if !result.IsError || !strings.Contains(writeGuardText(result), "mode") {
					t.Errorf("invalid mode not rejected first: %s", writeGuardText(result))
				}
			}
			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 0 {
				t.Errorf("invalid mode touched filesystem: %v %v", entries, err)
			}
		})
	}
}

func TestFileWriteCreateRaceHasOneWinner(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "writer", "sessions", "test")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		content string
		result  *mcp.CallToolResult
		err     error
	}
	const writers = 16
	outcomes := make(chan outcome, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		go func(i int) {
			<-start
			content := fmt.Sprintf("writer-%d", i)
			result, err := fileWrite(base)(writeGuardContext(), callWith(map[string]any{"path": "record.txt", "content": content, "mode": "create"}))
			outcomes <- outcome{content, result, err}
		}(i)
	}
	close(start)
	winners, winner := 0, ""
	for i := 0; i < writers; i++ {
		out := <-outcomes
		if out.err != nil {
			t.Fatal(out.err)
		}
		if !out.result.IsError {
			winners++
			winner = out.content
		}
	}
	if winners != 1 {
		t.Fatalf("got %d successful creators; want exactly one", winners)
	}
	assertWriteGuardFile(t, filepath.Join(root, "record.txt"), winner)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Errorf("create race generated backups: %v %v", entries, err)
	}
}

func TestFileWriteNewFileReportsCreateMode(t *testing.T) {
	base := t.TempDir()
	result, err := fileWrite(base)(writeGuardContext(), callWith(map[string]any{"path": "new/sub/file.txt", "content": "新文件"}))
	if err != nil || result.IsError {
		t.Fatalf("default create failed: %v %v", result, err)
	}
	if !strings.Contains(writeGuardText(result), "mode=create") {
		t.Errorf("missing mode: %s", writeGuardText(result))
	}
	assertWriteGuardFile(t, filepath.Join(base, "writer", "sessions", "test", "new", "sub", "file.txt"), "新文件")
}

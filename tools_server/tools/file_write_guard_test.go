package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
				args := map[string]any{"path": path, "_orka_history": "omitted content; file_read notes.md"}
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

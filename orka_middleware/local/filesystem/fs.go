// Package filesystem provides local file tools (read/write/list) that the
// control layer can use directly. All access is confined to a root directory
// via pathsafe (filepath.Rel containment, not HasPrefix).
package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orka-oss/orka_core/agent"
	"github.com/orka-oss/orka_core/pathsafe"
)

// New returns the file tool set rooted at root.
func New(root string) []agent.BaseTool {
	return []agent.BaseTool{
		readTool{root: root},
		writeTool{root: root},
		listTool{root: root},
	}
}

type readTool struct{ root string }

func (readTool) Name() string        { return "file_read" }
func (readTool) Description() string { return "Read a UTF-8 text file. Args: {path}." }
func (readTool) Schema() map[string]any {
	return objSchema(map[string]any{"path": strProp("relative file path")}, "path")
}
func (t readTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	p, err := pathsafe.Resolve(t.root, asString(args["path"]))
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	return string(b), nil
}

type writeTool struct{ root string }

func (writeTool) Name() string        { return "file_write" }
func (writeTool) Description() string { return "Write a UTF-8 text file (creates dirs). Args: {path, content}." }
func (writeTool) Schema() map[string]any {
	return objSchema(map[string]any{
		"path":    strProp("relative file path"),
		"content": strProp("file content"),
	}, "path", "content")
}
func (t writeTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	p, err := pathsafe.Resolve(t.root, asString(args["path"]))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(p, []byte(asString(args["content"])), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(asString(args["content"])), asString(args["path"])), nil
}

type listTool struct{ root string }

func (listTool) Name() string        { return "file_list" }
func (listTool) Description() string { return "List directory entries. Args: {path} (default root)." }
func (listTool) Schema() map[string]any {
	return objSchema(map[string]any{"path": strProp("relative directory path")})
}
func (t listTool) Invoke(_ context.Context, args map[string]any) (string, error) {
	rel := asString(args["path"])
	if rel == "" {
		rel = "."
	}
	p, err := pathsafe.Resolve(t.root, rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", fmt.Errorf("list: %w", err)
	}
	var sb strings.Builder
	for _, e := range entries {
		kind := "f"
		if e.IsDir() {
			kind = "d"
		}
		fmt.Fprintf(&sb, "%s\t%s\n", kind, e.Name())
	}
	return sb.String(), nil
}

// helpers

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

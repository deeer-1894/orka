// Package workspaceio owns the file-write contract used by both local and MCP
// adapters. Transport-specific identity and error envelopes stay in callers.
package workspaceio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orka-oss/orka_core/pathsafe"
)

const WriteDescription = "Write a UTF-8 text file (creates dirs). mode=create is the default and refuses an existing file without changing it. Use mode=append to add only new text at the end (no automatic newline); use file_read then mode=replace with the complete new content to overwrite. replace and append retain prior-version backups when available. Clients that previously overwrote without a mode must explicitly pass mode=replace."

// WriteProperties returns a fresh schema so callers cannot mutate shared state.
func WriteProperties() map[string]any {
	return map[string]any{
		"path":    map[string]any{"type": "string", "description": "relative file path"},
		"content": map[string]any{"type": "string", "description": "complete content for create/replace; only new text for append"},
		"mode":    map[string]any{"type": "string", "enum": []string{"create", "replace", "append"}, "default": "create", "description": "create: new file only; replace: complete overwrite; append: add text"},
	}
}

// Intent is validated before any directory creation or version backup.
// Apply validates it again so direct callers cannot bypass the contract.
type Intent struct{ Mode, Content string }

func ParseIntent(args map[string]any) (Intent, error) {
	mode := "create"
	if value, present := args["mode"]; present {
		var ok bool
		mode, ok = value.(string)
		if !ok || (mode != "create" && mode != "replace" && mode != "append") {
			return Intent{}, fmt.Errorf("mode must be create, replace, or append; omit it for create. No file was changed")
		}
	}
	content, ok := args["content"].(string)
	if !ok {
		return Intent{}, fmt.Errorf("content must be an explicit string; historical calls may omit it. Use file_read to retrieve existing content before editing; no file was changed")
	}
	trimmed := strings.TrimSpace(content)
	for _, tag := range []string{"persisted-arg", "persisted-output"} {
		if strings.HasPrefix(trimmed, "<"+tag+">") && strings.HasSuffix(trimmed, "</"+tag+">") {
			return Intent{}, fmt.Errorf("content is a context compression pointer, not file data. Use file_read to recover the content and retry with actual text; no file was changed")
		}
	}
	return Intent{mode, content}, nil
}

// NormalizePath retains the historical remote adapter's absolute-name fallback.
// Traversal and symlinks are checked by pathsafe and os.Root at use time.
func NormalizePath(rel string) string {
	rel = strings.TrimSpace(rel)
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return filepath.Base(strings.ReplaceAll(rel, "\\", "/"))
	}
	return rel
}

type WriteResult struct {
	Mode, Path, Version string
	Bytes               int
}

func (r WriteResult) String() string {
	msg := fmt.Sprintf("mode=%s wrote %d bytes to %s", r.Mode, r.Bytes, r.Path)
	if r.Version != "" {
		msg += " (previous version saved to history in " + TrashDir + ")"
	}
	return msg
}

// Apply pins operations to a directory handle; even concurrent symlink changes
// cannot escape that workspace. create uses O_EXCL and append uses O_APPEND.
// Arbitrary scripts and atomic artifact publication are separate contracts.
func Apply(rootPath, rel string, intent Intent) (WriteResult, error) {
	checked, err := ParseIntent(map[string]any{"mode": intent.Mode, "content": intent.Content})
	if err != nil {
		return WriteResult{}, err
	}
	rel = NormalizePath(rel)
	if rel == "" || rel == "." {
		return WriteResult{}, fmt.Errorf("path is required")
	}
	if _, err := pathsafe.Resolve(rootPath, rel); err != nil {
		return WriteResult{}, err
	}
	if err := os.MkdirAll(rootPath, pathsafe.WorkspaceDirMode); err != nil {
		return WriteResult{}, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return WriteResult{}, err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(rel), pathsafe.WorkspaceDirMode); err != nil {
		return WriteResult{}, err
	}
	result := WriteResult{Mode: checked.Mode, Path: rel}
	flags := os.O_WRONLY | os.O_CREATE
	switch checked.Mode {
	case "create":
		flags |= os.O_EXCL
	case "replace":
		flags |= os.O_TRUNC
	case "append":
		flags |= os.O_APPEND
	}
	if checked.Mode != "create" {
		if old, err := root.ReadFile(rel); err == nil {
			result.Version, _ = Snapshot(root, rel, old)
		}
	}
	file, err := root.OpenFile(rel, flags, pathsafe.WorkspaceFileMode)
	if err != nil {
		if checked.Mode == "create" && os.IsExist(err) {
			return result, fmt.Errorf("mode=create refused: file already exists; no file was changed and no backup was created. Use mode=append to add text, or file_read then mode=replace with the complete new file content to intentionally overwrite")
		}
		return result, err
	}
	result.Bytes, err = file.WriteString(checked.Content)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return result, fmt.Errorf("mode=%s wrote %d bytes to %s before error: %v; use file_read to inspect the file before retrying", checked.Mode, result.Bytes, rel, err)
	}
	return result, nil
}

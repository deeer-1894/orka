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

const WriteDescription = "Write a UTF-8 text file (creates dirs). mode=create is the default and refuses an existing file without changing it. To change part of an existing file use mode=edit: old_text is the exact current text (it must appear exactly once; include a few surrounding lines to make it unique) and content is what replaces it — far cheaper than rewriting the file. mode=append adds only new text at the end (no automatic newline); mode=replace overwrites with the complete new content (file_read first). edit, replace and append keep the prior version in history. Clients that previously overwrote without a mode must explicitly pass mode=replace."

// WriteProperties returns a fresh schema so callers cannot mutate shared state.
func WriteProperties() map[string]any {
	return map[string]any{
		"path":     map[string]any{"type": "string", "description": "relative file path"},
		"content":  map[string]any{"type": "string", "description": "complete content for create/replace; only new text for append; the replacement for old_text in edit"},
		"mode":     map[string]any{"type": "string", "enum": []string{"create", "edit", "replace", "append"}, "default": "create", "description": "create: new file only; edit: replace old_text once; replace: complete overwrite; append: add text"},
		"old_text": map[string]any{"type": "string", "description": "mode=edit only: exact text currently in the file, appearing exactly once"},
	}
}

// Intent is validated before any directory creation or version backup.
// Apply validates it again so direct callers cannot bypass the contract.
type Intent struct{ Mode, Content, OldText string }

func ParseIntent(args map[string]any) (Intent, error) {
	mode := "create"
	if value, present := args["mode"]; present {
		var ok bool
		mode, ok = value.(string)
		if !ok || (mode != "create" && mode != "edit" && mode != "replace" && mode != "append") {
			return Intent{}, fmt.Errorf("mode must be create, edit, replace, or append; omit it for create. No file was changed")
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
	intent := Intent{Mode: mode, Content: content}
	if mode == "edit" {
		old, _ := args["old_text"].(string)
		if old == "" {
			return Intent{}, fmt.Errorf("mode=edit needs old_text: the exact text to replace. No file was changed")
		}
		intent.OldText = old
	}
	return intent, nil
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
	checked, err := ParseIntent(map[string]any{"mode": intent.Mode, "content": intent.Content, "old_text": intent.OldText})
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
	if checked.Mode == "edit" {
		return applyEdit(root, rel, checked, result)
	}
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

// applyEdit replaces the one occurrence of OldText. Zero or several matches
// change nothing: guessing which one was meant is how edits corrupt files.
func applyEdit(root *os.Root, rel string, intent Intent, result WriteResult) (WriteResult, error) {
	old, err := root.ReadFile(rel)
	if err != nil {
		if os.IsNotExist(err) {
			return result, fmt.Errorf("mode=edit needs an existing file; %s does not exist. Use mode=create for a new file", rel)
		}
		return result, err
	}
	switch n := strings.Count(string(old), intent.OldText); n {
	case 0:
		return result, fmt.Errorf("old_text was not found in %s; no file was changed. Use file_read to copy the current text exactly (including whitespace)", rel)
	case 1:
	default:
		return result, fmt.Errorf("old_text appears %d times in %s; no file was changed. Include more surrounding lines so it matches exactly once", n, rel)
	}
	updated := strings.Replace(string(old), intent.OldText, intent.Content, 1)
	result.Version, _ = Snapshot(root, rel, old)
	if err := root.WriteFile(rel, []byte(updated), pathsafe.WorkspaceFileMode); err != nil {
		return result, fmt.Errorf("mode=edit failed on %s: %v; use file_read to inspect the file before retrying", rel, err)
	}
	result.Bytes = len(updated)
	return result, nil
}

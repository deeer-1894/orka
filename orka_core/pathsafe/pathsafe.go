// Package pathsafe provides path containment checks shared by the file tools.
// It uses filepath.Rel (not strings.HasPrefix) to robustly reject traversal
// outside an allowed root, including ".." escapes and absolute-path injection.
package pathsafe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrEscapes is returned when a path would resolve outside the root.
var ErrEscapes = errors.New("pathsafe: path escapes the allowed root")

// Resolve joins rel onto root and returns the cleaned absolute path, but only
// if it stays within root. filepath.Join naturally absorbs a leading separator
// (so an absolute-looking rel is confined to root rather than escaping), while
// genuine ".." traversal that escapes root is rejected via filepath.Rel.
func Resolve(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("pathsafe: bad root: %w", err)
	}
	joined := filepath.Join(absRoot, rel)

	r, err := filepath.Rel(absRoot, joined)
	if err != nil {
		return "", ErrEscapes
	}
	if r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", ErrEscapes
	}
	// Refuse symlinks in every existing component, including the root itself.
	// Callers needing protection against concurrent renames must also use os.Root.
	for p := joined; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", ErrEscapes
		}
		if parent := filepath.Dir(p); parent == p {
			break
		}
	}
	return joined, nil
}

// UserRoot returns the per-user storage root base/<user>, where user is
// sanitized to a single path segment.
func UserRoot(base, user string) string {
	if user == "" {
		user = "_anonymous"
	}
	// keep a single safe segment: drop separators and traversal
	user = strings.ReplaceAll(user, "/", "_")
	user = strings.ReplaceAll(user, "\\", "_")
	user = strings.ReplaceAll(user, "..", "_")
	return filepath.Join(filepath.Clean(base), user)
}

// SessionRoot returns a workspace for a trusted owner and conversation. Missing
// or malformed context is an error; it must never fall back to the user root.
func SessionRoot(base, user, conversationID string) (string, error) {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(user) == "" ||
		user != strings.TrimSpace(user) || user == "." || strings.Contains(user, "..") || strings.ContainsAny(user, "/\\\x00") {
		return "", fmt.Errorf("workspace requires a valid owner and storage base")
	}
	if conversationID == "" || len(conversationID) > 128 {
		return "", fmt.Errorf("conversation_id required (maximum 128 characters)")
	}
	for _, c := range conversationID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return "", fmt.Errorf("invalid conversation_id")
		}
	}
	return Resolve(UserRoot(base, user), filepath.Join("sessions", conversationID))
}

// EnsureSession creates an empty session workspace without moving legacy files.
func EnsureSession(base, user, conversationID string) (string, error) {
	root, err := SessionRoot(base, user, conversationID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, WorkspaceDirMode); err != nil {
		return "", err
	}
	return root, nil
}

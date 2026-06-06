// Package pathsafe provides path containment checks shared by the file tools.
// It uses filepath.Rel (not strings.HasPrefix) to robustly reject traversal
// outside an allowed root, including ".." escapes and absolute-path injection.
package pathsafe

import (
	"errors"
	"fmt"
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

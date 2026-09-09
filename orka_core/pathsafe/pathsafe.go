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
// sanitized to a single path segment. This is the ACCOUNT root: things that
// belong to the person rather than to one piece of work (the quant factor
// library, run journals) live here.
func UserRoot(base, user string) string {
	if user == "" {
		user = "_anonymous"
	}
	return filepath.Join(filepath.Clean(base), segment(user))
}

// Workspace returns the storage root an agent run actually works in:
// base/<user>/<conv>. One conversation's files are invisible to another, which
// is what stops an account's workspace from silently becoming a shared dumping
// ground — every run listing the root used to see every file every other run
// had ever produced, and name collisions across unrelated tasks were routine.
//
// An empty conv falls back to the account root. That is not a per-conversation
// workspace, and is deliberate: a scheduled task or a quant pipeline has no
// conversation, and giving each invocation its own directory would scatter
// their output instead of accumulating it where the next run expects it.
func Workspace(base, user, conv string) string {
	root := UserRoot(base, user)
	if strings.TrimSpace(conv) == "" {
		return root
	}
	return filepath.Join(root, segment(conv))
}

// segment reduces s to a single safe path component: no separators, no
// traversal. Containment is still enforced by Resolve — this only keeps a
// hostile identifier from spanning directories in the first place.
func segment(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "..", "_")
	return s
}

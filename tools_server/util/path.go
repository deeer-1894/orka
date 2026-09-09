// Package util holds gateway path helpers.
package util

import (
	"path"
	"strings"

	"github.com/orka-oss/orka_core/pathsafe"
)

// ResolvePath confines rel to root using filepath.Rel containment (never
// HasPrefix). Returns an error on traversal.
//
// It takes the resolved ROOT rather than (base, user): the workspace is now
// per-conversation, and a helper that rebuilt the root from an email would
// quietly hand back the account root instead — writing one conversation's file
// where every other conversation can see it. Callers get the root from
// identity.Identity.Root, which is the only thing that knows both halves.
//
// Models frequently pass an absolute-looking path (e.g.
// "/root/.openclaw/workspace/report.md") thinking it's the workspace root.
// Rather than nest that whole path under the sandbox, collapse an absolute path
// to its base filename so the artifact lands cleanly at the workspace root.
func ResolvePath(root, rel string) (string, error) {
	return pathsafe.Resolve(root, normalizeRel(rel))
}

func normalizeRel(rel string) string {
	rel = strings.TrimSpace(rel)
	// Absolute path → keep only the filename (the model meant "at the root").
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return path.Base(strings.ReplaceAll(rel, "\\", "/"))
	}
	return rel
}

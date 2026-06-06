// Package util holds gateway path helpers.
package util

import "github.com/cavis-oss/cavis_core/pathsafe"

// ResolvePath confines rel to the per-user root base/<user> using filepath.Rel
// containment (never HasPrefix). Returns an error on traversal.
func ResolvePath(base, user, rel string) (string, error) {
	return pathsafe.Resolve(pathsafe.UserRoot(base, user), rel)
}

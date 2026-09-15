package tools

import (
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/workspaceio"
	"os"
	"path/filepath"
)

const TrashDir = workspaceio.TrashDir
const stampFormat = workspaceio.VersionFormat
const maxVersionsPerFile = workspaceio.MaxVersionsPerFile

// Adapter retained for render_report; history storage belongs to workspaceio.
func backupBeforeWrite(base, user, rel string, conversationID ...string) bool {
	rootPath := pathsafe.UserRoot(base, user)
	if len(conversationID) > 0 {
		var err error
		rootPath, err = pathsafe.SessionRoot(base, user, conversationID[0])
		if err != nil {
			return false
		}
	}
	rel = workspaceio.NormalizePath(rel)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return false
	}
	defer root.Close()
	content, err := root.ReadFile(rel)
	if err != nil {
		return false
	}
	stamp, err := workspaceio.Snapshot(root, filepath.Clean(rel), content)
	return err == nil && stamp != ""
}

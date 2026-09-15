package workspaceio

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/pathsafe"
)

const TrashDir = ".orka_trash"
const VersionFormat = "20060102-150405.000000000"
const LegacyVersionFormat = "20060102-150405"
const MaxVersionsPerFile = 20

// ParseVersion accepts exactly the two directory formats emitted by Orka.
// Strict shape checking prevents timestamp input being used as a path.
func ParseVersion(stamp string) (time.Time, error) {
	layout := VersionFormat
	if len(stamp) == len(LegacyVersionFormat) {
		layout = LegacyVersionFormat
	}
	if len(stamp) != len(layout) {
		return time.Time{}, fmt.Errorf("invalid version timestamp")
	}
	parsed, err := time.ParseInLocation(layout, stamp, time.Local)
	if err != nil || parsed.Format(layout) != stamp {
		return time.Time{}, fmt.Errorf("invalid version timestamp")
	}
	return parsed, nil
}

// Snapshot stores a prior version. Callers explicitly choose whether a backup
// failure blocks their write; file_write retains its best-effort compatibility.
// Reserving each timestamp directory atomically avoids collisions across processes.
func Snapshot(root *os.Root, rel string, content []byte) (string, error) {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", pathsafe.ErrEscapes
	}
	if rel == TrashDir || strings.HasPrefix(filepath.ToSlash(rel), TrashDir+"/") {
		return "", nil
	}
	if err := root.MkdirAll(TrashDir, pathsafe.WorkspaceDirMode); err != nil {
		return "", err
	}
	stampTime := time.Now()
	for attempts := 0; attempts < 100; attempts++ {
		stamp := stampTime.Add(time.Duration(attempts)).Format(VersionFormat)
		dir := filepath.Join(TrashDir, stamp)
		if err := root.Mkdir(dir, pathsafe.WorkspaceDirMode); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		dst := filepath.Join(dir, rel)
		if err := root.MkdirAll(filepath.Dir(dst), pathsafe.WorkspaceDirMode); err != nil {
			return "", err
		}
		if err := root.WriteFile(dst, content, pathsafe.WorkspaceFileMode); err != nil {
			return "", err
		}
		Prune(root, rel, MaxVersionsPerFile)
		return stamp, nil
	}
	return "", fmt.Errorf("could not reserve version timestamp")
}

// Prune only removes this file's oldest known versions through the pinned root.
func Prune(root *os.Root, rel string, keep int) {
	if keep < 0 {
		return
	}
	dir, err := root.Open(TrashDir)
	if err != nil {
		return
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return
	}
	var stamps []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := ParseVersion(entry.Name()); err != nil {
			continue
		}
		info, err := root.Stat(filepath.Join(TrashDir, entry.Name(), rel))
		if err == nil && info.Mode().IsRegular() {
			stamps = append(stamps, entry.Name())
		}
	}
	sort.Strings(stamps)
	for i := 0; i < len(stamps)-keep; i++ {
		_ = root.Remove(filepath.Join(TrashDir, stamps[i], rel))
	}
}

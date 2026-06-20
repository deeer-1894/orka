package tools

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orka-oss/tools_server/util"
)

// TrashDir holds timestamped backups of overwritten files, so any destructive
// write (file_write, the engineer agent, doc tools…) is recoverable and
// diffable. One subdir per overwrite: .orka_trash/<stamp>/<rel>.
const TrashDir = ".orka_trash"

// stampFormat keys each backup. Nanosecond precision so rapid successive writes
// to the same file (an editing agent) don't collide in the same dir and clobber
// each other. Fixed width → lexical sort is chronological. The control layer
// parses the same layout (keep them in sync).
const stampFormat = "20060102-150405.000000000"

// backupBeforeWrite copies the current contents of rel (if it exists) into the
// trash before it is overwritten. Best-effort: any failure returns false and
// never blocks the write. Paths already under the trash are skipped so backups
// never recurse on themselves.
func backupBeforeWrite(base, user, rel string) bool {
	clean := filepath.ToSlash(strings.TrimSpace(rel))
	if clean == TrashDir || strings.HasPrefix(clean, TrashDir+"/") {
		return false
	}
	src, err := util.ResolvePath(base, user, rel)
	if err != nil {
		return false
	}
	old, err := os.ReadFile(src)
	if err != nil {
		return false // nothing to back up (new file or unreadable)
	}
	ts := time.Now().Format(stampFormat)
	dst, err := util.ResolvePath(base, user, filepath.Join(TrashDir, ts, rel))
	if err != nil {
		return false
	}
	if os.MkdirAll(filepath.Dir(dst), 0o755) != nil {
		return false
	}
	if os.WriteFile(dst, old, 0o644) != nil {
		return false
	}
	pruneVersions(base, user, rel, maxVersionsPerFile)
	return true
}

// maxVersionsPerFile caps the snapshots kept per file so the trash stays bounded.
const maxVersionsPerFile = 20

// pruneVersions deletes the oldest snapshots of rel beyond keep (best-effort).
func pruneVersions(base, user, rel string, keep int) {
	trashRoot, err := util.ResolvePath(base, user, TrashDir)
	if err != nil {
		return
	}
	stamps, err := os.ReadDir(trashRoot)
	if err != nil {
		return
	}
	var have []string // stamp dirs that hold a version of rel, sorted oldest→newest
	for _, s := range stamps {
		if !s.IsDir() {
			continue
		}
		vp := filepath.Join(trashRoot, s.Name(), filepath.FromSlash(rel))
		if fi, e := os.Stat(vp); e == nil && !fi.IsDir() {
			have = append(have, s.Name())
		}
	}
	sort.Strings(have) // timestamp names sort chronologically
	for i := 0; i < len(have)-keep; i++ {
		_ = os.Remove(filepath.Join(trashRoot, have[i], filepath.FromSlash(rel)))
	}
}

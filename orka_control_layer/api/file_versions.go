package api

import (
	"context"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/workspaceio"
	"os"
	"path/filepath"
	"sort"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// trashDir mirrors tools_server's TrashDir: the gateway writes a timestamped
// copy of every overwritten file here, giving the control layer a version
// history to list, diff (client-side), and restore.
const trashDir = workspaceio.TrashDir

// stampFormat must match tools_server's: nanosecond backup-dir timestamps.
const stampFormat = workspaceio.VersionFormat

// fileVersion is one historical snapshot of a file.
type fileVersion struct {
	TS   string `json:"ts"`   // backup folder name, e.g. 20260619-014233
	When int64  `json:"when"` // unix millis parsed from TS (for display)
	Size int64  `json:"size"` // bytes of that snapshot
	Path string `json:"path"` // download path for this version
}

// FileVersions lists prior versions of a file from the trash, newest first.
func (a *API) FileVersions(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Path string `json:"path"`
	}
	if err := bind(c, &req); err != nil || req.Path == "" {
		fail(c, consts.StatusBadRequest, "path required")
		return
	}
	rel, err := fileRel(req.Path)
	if err != nil || rel == "." {
		fail(c, 400, "invalid path")
		return
	}
	root, _, err := a.openWorkspace(ctx, c, false)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	trash, err := root.Open(trashDir)
	var stamps []os.DirEntry
	if err == nil {
		defer trash.Close()
		stamps, _ = trash.ReadDir(-1)
	} // // missing trash → no versions, not an error
	out := make([]fileVersion, 0, len(stamps))
	for _, s := range stamps {
		if !s.IsDir() {
			continue
		}
		vp, rerr := fileRel(filepath.Join(trashDir, s.Name(), rel))
		if rerr != nil {
			continue
		}
		info, serr := root.Stat(vp)
		if serr != nil || info.IsDir() {
			continue
		}
		out = append(out, fileVersion{
			TS:   s.Name(),
			When: parseStampMillis(s.Name()),
			Size: info.Size(),
			Path: filepath.ToSlash(filepath.Join(trashDir, s.Name(), rel)),
		})
	}
	// Newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	ok(c, out)
}

// FileRestore overwrites a file with one of its historical versions. The current
// contents are backed up first, so a restore is itself reversible.
func (a *API) FileRestore(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Path string `json:"path"`
		TS   string `json:"ts"`
	}
	if err := bind(c, &req); err != nil || req.Path == "" || req.TS == "" {
		fail(c, consts.StatusBadRequest, "path and ts required")
		return
	}
	rel, err := fileRel(req.Path)
	if err != nil || rel == "." {
		fail(c, 400, "invalid path")
		return
	}
	if _, err := workspaceio.ParseVersion(req.TS); err != nil {
		fail(c, 400, "invalid version timestamp")
		return
	}
	root, _, err := a.openWorkspace(ctx, c, true)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	versionPath, err := fileRel(filepath.Join(trashDir, req.TS, rel))
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	data, err := root.ReadFile(versionPath)
	if err != nil {
		fail(c, consts.StatusNotFound, "version not found")
		return
	}
	cur, err := fileRel(rel)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	// Snapshot the current contents before clobbering them (reversible restore).
	if old, rerr := root.ReadFile(cur); rerr == nil {
		snapshotToTrash(root, rel, old)
	}
	if err := root.MkdirAll(filepath.Dir(cur), pathsafe.WorkspaceDirMode); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	if err := root.WriteFile(cur, data, pathsafe.WorkspaceFileMode); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	ok(c, map[string]any{"restored": rel, "from": req.TS, "size": len(data)})
}

// snapshotToTrash writes content to .orka_trash/<now>/<rel> (best-effort).
func snapshotToTrash(root *os.Root, rel string, content []byte) {
	_, _ = workspaceio.Snapshot(root, rel, content)
}

// parseStampMillis turns a 20060102-150405 trash folder name into unix millis;
// 0 if it doesn't parse (the TS still sorts lexically).
func parseStampMillis(stamp string) int64 {
	t, err := workspaceio.ParseVersion(stamp)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

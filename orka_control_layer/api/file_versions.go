package api

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// trashDir mirrors tools_server's TrashDir: the gateway writes a timestamped
// copy of every overwritten file here, giving the control layer a version
// history to list, diff (client-side), and restore.
const trashDir = ".orka_trash"

// stampFormat must match tools_server's: nanosecond backup-dir timestamps.
const stampFormat = "20060102-150405.000000000"

// fileVersion is one historical snapshot of a file.
type fileVersion struct {
	TS    string `json:"ts"`    // backup folder name, e.g. 20260619-014233
	When  int64  `json:"when"`  // unix millis parsed from TS (for display)
	Size  int64  `json:"size"`  // bytes of that snapshot
	Path  string `json:"path"`  // download path for this version
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
	rel := filepath.Clean(req.Path)
	trashRoot, err := a.resolve(c, trashDir)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	stamps, _ := os.ReadDir(trashRoot) // missing trash → no versions, not an error
	out := make([]fileVersion, 0, len(stamps))
	for _, s := range stamps {
		if !s.IsDir() {
			continue
		}
		vp, rerr := a.resolve(c, filepath.Join(trashDir, s.Name(), rel))
		if rerr != nil {
			continue
		}
		info, serr := os.Stat(vp)
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
	rel := filepath.Clean(req.Path)
	versionPath, err := a.resolve(c, filepath.Join(trashDir, filepath.Clean(req.TS), rel))
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(versionPath)
	if err != nil {
		fail(c, consts.StatusNotFound, "version not found")
		return
	}
	cur, err := a.resolve(c, rel)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	// Snapshot the current contents before clobbering them (reversible restore).
	if old, rerr := os.ReadFile(cur); rerr == nil {
		a.snapshotToTrash(c, rel, old)
	}
	if err := os.MkdirAll(filepath.Dir(cur), 0o755); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(cur, data, 0o644); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	ok(c, map[string]any{"restored": rel, "from": req.TS, "size": len(data)})
}

// snapshotToTrash writes content to .orka_trash/<now>/<rel> (best-effort).
func (a *API) snapshotToTrash(c *app.RequestContext, rel string, content []byte) {
	dst, err := a.resolve(c, filepath.Join(trashDir, time.Now().Format(stampFormat), rel))
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(dst), 0o755) == nil {
		_ = os.WriteFile(dst, content, 0o644)
	}
}

// parseStampMillis turns a 20060102-150405 trash folder name into unix millis;
// 0 if it doesn't parse (the TS still sorts lexically).
func parseStampMillis(stamp string) int64 {
	t, err := time.ParseInLocation(stampFormat, stamp, time.Local)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

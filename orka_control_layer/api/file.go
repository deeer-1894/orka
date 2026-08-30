package api

import (
	"context"
	"encoding/base64"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_core/pathsafe"
)

// userRoot resolves the caller's storage root from the authenticated identity.
func (a *API) userRoot(c *app.RequestContext) string {
	return pathsafe.UserRoot(a.BaseStorage, authEmail(c))
}

func (a *API) resolve(c *app.RequestContext, rel string) (string, error) {
	return pathsafe.Resolve(a.userRoot(c), rel)
}

// FileUpload accepts a multipart "file" and stores it under {dir}/{filename}.
func (a *API) FileUpload(ctx context.Context, c *app.RequestContext) {
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, consts.StatusBadRequest, "missing file: "+err.Error())
		return
	}
	rel := filepath.Join(string(c.FormValue("dir")), fh.Filename)
	dst, err := a.resolve(c, rel)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	if err := c.SaveUploadedFile(fh, dst); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	ok(c, map[string]any{"path": rel, "size": fh.Size})
}

// FileDownload streams a file by ?path=. An optional ?conv=<id> lets a user who
// can read a shared conversation fetch files from that conversation's OWNER
// workspace (read-only); without it, files resolve under the caller's own root.
func (a *API) FileDownload(ctx context.Context, c *app.RequestContext) {
	root := a.userRoot(c)
	if convID := string(c.Query("conv")); convID != "" {
		conv, err := a.Store.GetConversation(ctx, convID)
		if err != nil || !conv.CanRead(authEmail(c)) {
			fail(c, consts.StatusNotFound, "not found")
			return
		}
		root = pathsafe.UserRoot(a.BaseStorage, conv.OwnerEmail)
	}
	p, err := pathsafe.Resolve(root, string(c.Query("path")))
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		fail(c, consts.StatusNotFound, "not found")
		return
	}
	// Serve the bytes directly. We deliberately avoid c.File(), whose Hertz
	// static handler gzip-caches a "<name>.hertz.gz" sibling next to the file —
	// littering the user's workspace with junk artifacts on every download.
	ct := mime.TypeByExtension(filepath.Ext(p))
	if ct == "" {
		ct = fallbackContentType(filepath.Ext(p))
	}
	// `inline=1` lets the web file-preview render the bytes in-page (e.g. a PDF in
	// an <iframe>); the default stays `attachment` so the "下载" links save a file.
	disp := "attachment"
	if string(c.Query("inline")) == "1" {
		disp = "inline"
	}
	c.Response.Header.Set("Content-Disposition", contentDisposition(disp, filepath.Base(p)))
	c.Data(consts.StatusOK, ct, data)
}

// contentDisposition builds an RFC 6266 header. HTTP header values are ASCII, so
// a Chinese (or any non-ASCII) filename written raw makes the whole response
// malformed — Chrome rejects it outright ("响应无效" / "Failed to fetch"), which
// broke preview and download for every non-ASCII filename. Emit an ASCII
// fallback plus the real name in the filename* form.
func contentDisposition(disp, name string) string {
	ascii := make([]rune, 0, len(name))
	nonASCII := false
	for _, r := range name {
		switch {
		case r < 32 || r == '"' || r == '\\' || r == 127:
			ascii = append(ascii, '_') // never let a quote break the header
		case r > 127:
			nonASCII = true
			ascii = append(ascii, '_')
		default:
			ascii = append(ascii, r)
		}
	}
	h := disp + "; filename=\"" + string(ascii) + "\""
	if nonASCII {
		h += "; filename*=UTF-8''" + url.PathEscape(name)
	}
	return h
}

// fallbackContentType covers the text formats the agent produces that Go's mime
// table does not know (notably .md), so the browser renders them inline instead
// of treating them as opaque downloads.
func fallbackContentType(ext string) string {
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log", ".csv", ".tsv", ".yaml", ".yml", ".ini", ".conf":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	}
	return "application/octet-stream"
}

// FileList lists a directory.
func (a *API) FileList(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Path string `json:"path"`
	}
	_ = bind(c, &req)
	if req.Path == "" {
		req.Path = "."
	}
	p, err := a.resolve(c, req.Path)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		fail(c, consts.StatusNotFound, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		var size, mtime int64
		if info != nil {
			size = info.Size()
			mtime = info.ModTime().UnixMilli()
		}
		out = append(out, map[string]any{"name": e.Name(), "dir": e.IsDir(), "size": size, "mtime": mtime})
	}
	ok(c, out)
}

// FileDelete removes a file.
func (a *API) FileDelete(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Path string `json:"path"`
	}
	if err := bind(c, &req); err != nil || req.Path == "" {
		fail(c, consts.StatusBadRequest, "path required")
		return
	}
	p, err := a.resolve(c, req.Path)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	// RemoveAll deletes a file or a (non-empty) directory, and is idempotent if
	// the path is already gone — both are the right semantics for a file-manager
	// delete. resolve() has confined p to the caller's workspace root.
	if err := os.RemoveAll(p); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	ok(c, map[string]string{"deleted": req.Path})
}

// GetFileURL returns the download URL for a stored file.
func (a *API) GetFileURL(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Path string `json:"path"`
	}
	if err := bind(c, &req); err != nil || req.Path == "" {
		fail(c, consts.StatusBadRequest, "path required")
		return
	}
	if _, err := a.resolve(c, req.Path); err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	ok(c, map[string]string{"url": "/api/v1/controller/file/download?path=" + req.Path})
}

// ---- resumable chunked upload ----

type chunkUploadReq struct {
	UploadID string `json:"upload_id"`
	Filename string `json:"filename"`
	Index    int    `json:"index"`
	Total    int    `json:"total"`
	Data     string `json:"data"` // base64 chunk
}

// FileUploadChunk accepts one chunk; assembles the file once all are received.
func (a *API) FileUploadChunk(ctx context.Context, c *app.RequestContext) {
	var req chunkUploadReq
	if err := bind(c, &req); err != nil || req.UploadID == "" || req.Total <= 0 {
		fail(c, consts.StatusBadRequest, "upload_id, total required")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		fail(c, consts.StatusBadRequest, "bad base64 data")
		return
	}
	complete, received := a.chunks.add(req.UploadID, req.Filename, req.Total, req.Index, raw)
	if !complete {
		ok(c, map[string]any{"received": received, "total": req.Total, "complete": false})
		return
	}
	// assemble
	data, filename := a.chunks.assemble(req.UploadID)
	dst, err := a.resolve(c, filename)
	if err != nil {
		fail(c, consts.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		fail(c, consts.StatusInternalServerError, err.Error())
		return
	}
	a.chunks.drop(req.UploadID)
	ok(c, map[string]any{"path": filename, "size": len(data), "complete": true})
}

// FileUploadProgress reports received/total for an in-flight upload.
func (a *API) FileUploadProgress(ctx context.Context, c *app.RequestContext) {
	id := string(c.Query("upload_id"))
	received, total, ok2 := a.chunks.progress(id)
	if !ok2 {
		fail(c, consts.StatusNotFound, "unknown upload_id")
		return
	}
	ok(c, map[string]any{"received": received, "total": total})
}

// chunkManager tracks in-flight resumable uploads in memory.
type chunkManager struct {
	mu sync.Mutex
	m  map[string]*chunkAgg
}

type chunkAgg struct {
	filename string
	total    int
	parts    map[int][]byte
}

func newChunkManager() *chunkManager { return &chunkManager{m: map[string]*chunkAgg{}} }

// add stores a chunk; returns whether all chunks are present and the count.
func (cm *chunkManager) add(id, filename string, total, index int, data []byte) (bool, int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	agg, ok := cm.m[id]
	if !ok {
		agg = &chunkAgg{filename: filename, total: total, parts: map[int][]byte{}}
		cm.m[id] = agg
	}
	if filename != "" {
		agg.filename = filename
	}
	agg.parts[index] = data
	return len(agg.parts) >= agg.total, len(agg.parts)
}

func (cm *chunkManager) assemble(id string) ([]byte, string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	agg, ok := cm.m[id]
	if !ok {
		return nil, ""
	}
	var out []byte
	for i := 0; i < agg.total; i++ {
		out = append(out, agg.parts[i]...)
	}
	return out, agg.filename
}

func (cm *chunkManager) progress(id string) (int, int, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	agg, ok := cm.m[id]
	if !ok {
		return 0, 0, false
	}
	return len(agg.parts), agg.total, true
}

func (cm *chunkManager) drop(id string) {
	cm.mu.Lock()
	delete(cm.m, id)
	cm.mu.Unlock()
}

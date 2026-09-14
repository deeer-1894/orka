package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/pathsafe"
)

// workspace authorizes conversation access before resolving its owner's session.
// Query `conv` and JSON/multipart `conversation_id` must agree when both exist.
func (a *API) workspace(ctx context.Context, c *app.RequestContext, write bool) (string, string, error) {
	email := authEmail(c)
	if email == "" {
		return "", "", &workspaceError{401, "login required"}
	}
	conv := string(c.Query("conv"))
	bodyID := ""
	if strings.Contains(string(c.Request.Header.ContentType()), "multipart/form-data") {
		bodyID = string(c.FormValue("conversation_id"))
	} else if len(c.Request.Body()) > 0 {
		var body struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
			return "", "", &workspaceError{400, "invalid request body"}
		}
		bodyID = body.ConversationID
	}
	if conv != "" && bodyID != "" && conv != bodyID {
		return "", "", &workspaceError{400, "conflicting conversation_id"}
	}
	if conv == "" {
		conv = bodyID
	}
	record, err := a.authorizedConversation(ctx, c, conv, write)
	if err != nil {
		return "", "", err
	}
	root, err := pathsafe.EnsureSession(a.BaseStorage, record.OwnerEmail, conv)
	return root, conv, err
}

func (a *API) authorizedConversation(ctx context.Context, c *app.RequestContext, conv string, write bool) (*db.ConversationTable, error) {
	email := authEmail(c)
	if email == "" {
		return nil, &workspaceError{401, "login required"}
	}
	if _, err := pathsafe.SessionRoot(a.BaseStorage, email, conv); err != nil {
		return nil, &workspaceError{400, err.Error()}
	}
	record, err := a.Store.GetConversation(ctx, conv)
	if err != nil || record == nil || record.OwnerEmail == "" || !record.CanRead(email) {
		return nil, &workspaceError{404, "conversation not found"}
	}
	if write && !record.CanWrite(email) {
		return nil, &workspaceError{403, "conversation is read-only"}
	}
	return record, nil
}

type workspaceError struct {
	status  int
	message string
}

func (e *workspaceError) Error() string { return e.message }
func workspaceFail(c *app.RequestContext, err error) {
	if e, ok := err.(*workspaceError); ok {
		fail(c, e.status, e.message)
		return
	}
	fail(c, consts.StatusBadRequest, err.Error())
}

// openWorkspace binds subsequent filesystem operations to a directory handle,
// preventing symlink/rename races from escaping the authorized session.
func (a *API) openWorkspace(ctx context.Context, c *app.RequestContext, write bool) (*os.Root, string, error) {
	root, conv, err := a.workspace(ctx, c, write)
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(root)
	return r, conv, err
}
func fileRel(rel string) (string, error) {
	if strings.ContainsAny(rel, "\\\x00") || filepath.IsAbs(rel) {
		return "", pathsafe.ErrEscapes
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", pathsafe.ErrEscapes
	}
	return clean, nil
}

// FileUpload accepts a multipart "file" and stores it under {dir}/{filename}.
func (a *API) FileUpload(ctx context.Context, c *app.RequestContext) {
	root, _, err := a.openWorkspace(ctx, c, true)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "missing file")
		return
	}
	dir, err := fileRel(string(c.FormValue("dir")))
	if err != nil {
		workspaceFail(c, err)
		return
	}
	rel, err := fileRel(filepath.Join(dir, fh.Filename))
	if err != nil || rel == "." {
		fail(c, 400, "invalid filename")
		return
	}
	src, err := fh.Open()
	if err != nil {
		fail(c, 400, "cannot read upload")
		return
	}
	defer src.Close()
	if err = root.MkdirAll(filepath.Dir(rel), pathsafe.WorkspaceDirMode); err != nil {
		workspaceFail(c, err)
		return
	}
	dst, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, pathsafe.WorkspaceFileMode)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		fail(c, 500, "upload failed")
		return
	}
	ok(c, map[string]any{"path": rel, "size": fh.Size})
}

// FileDownload streams a file from an authorized conversation workspace.
func (a *API) FileDownload(ctx context.Context, c *app.RequestContext) {
	root, _, err := a.openWorkspace(ctx, c, false)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	p, err := fileRel(string(c.Query("path")))
	if err != nil {
		workspaceFail(c, err)
		return
	}
	data, err := root.ReadFile(p)
	if err != nil {
		fail(c, 404, "not found")
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
	if err := bind(c, &req); err != nil {
		fail(c, 400, "invalid request body")
		return
	}
	if req.Path == "" {
		req.Path = "."
	}
	root, _, err := a.openWorkspace(ctx, c, false)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	p, err := fileRel(req.Path)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	dir, err := root.Open(p)
	if err != nil {
		fail(c, 404, "not found")
		return
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
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
	root, _, err := a.openWorkspace(ctx, c, true)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	p, err := fileRel(req.Path)
	if err != nil || p == "." {
		fail(c, 400, "cannot delete workspace root or invalid path")
		return
	}
	// RemoveAll deletes a file or a (non-empty) directory, and is idempotent if
	// the path is already gone — both are the right semantics for a file-manager
	// delete. resolve() has confined p to the caller's workspace root.
	if err := root.RemoveAll(p); err != nil {
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
	root, conv, err := a.openWorkspace(ctx, c, false)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	p, err := fileRel(req.Path)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	if _, err = root.Stat(p); err != nil {
		fail(c, 404, "not found")
		return
	}
	q := url.Values{"path": {p}, "conv": {conv}}
	ok(c, map[string]string{"url": "/api/v1/controller/file/download?" + q.Encode()})
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
	root, conv, err := a.openWorkspace(ctx, c, true)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	defer root.Close()
	var req chunkUploadReq
	if err := bind(c, &req); err != nil || req.UploadID == "" || len(req.UploadID) > 128 || req.Total <= 0 || req.Total > 10000 || req.Index < 0 || req.Index >= req.Total {
		fail(c, 400, "invalid chunk metadata")
		return
	}
	filename, err := fileRel(req.Filename)
	if err != nil || filename == "." {
		fail(c, 400, "invalid filename")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(raw) > 8<<20 {
		fail(c, 400, "invalid or oversized chunk")
		return
	}
	key := uploadKey(authEmail(c), conv, req.UploadID)
	complete, received, data, err := a.chunks.accept(key, filename, req.Total, req.Index, raw)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	if !complete {
		ok(c, map[string]any{"received": received, "total": req.Total, "complete": false})
		return
	}
	if err := root.MkdirAll(filepath.Dir(filename), pathsafe.WorkspaceDirMode); err != nil {
		workspaceFail(c, err)
		return
	}
	if err := root.WriteFile(filename, data, pathsafe.WorkspaceFileMode); err != nil {
		workspaceFail(c, err)
		return
	}
	a.chunks.drop(key)
	ok(c, map[string]any{"path": filename, "size": len(data), "complete": true})
}

func uploadKey(email, conv, id string) string {
	b, _ := json.Marshal([]string{email, conv, id})
	return string(b)
}

// FileUploadProgress never reveals another user's or conversation's upload.
func (a *API) FileUploadProgress(ctx context.Context, c *app.RequestContext) {
	_, conv, err := a.workspace(ctx, c, true)
	if err != nil {
		workspaceFail(c, err)
		return
	}
	key := uploadKey(authEmail(c), conv, string(c.Query("upload_id")))
	received, total, found := a.chunks.progress(key)
	if !found {
		fail(c, 404, "unknown upload_id")
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

// accept validates immutable upload metadata and assembles under the same lock.
func (cm *chunkManager) accept(id, filename string, total, index int, data []byte) (bool, int, []byte, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if total <= 0 || index < 0 || index >= total {
		return false, 0, nil, fmt.Errorf("invalid chunk index")
	}
	agg, found := cm.m[id]
	if !found {
		agg = &chunkAgg{filename: filename, total: total, parts: map[int][]byte{}}
		cm.m[id] = agg
	}
	if agg.filename != filename || agg.total != total {
		return false, len(agg.parts), nil, fmt.Errorf("upload metadata changed")
	}
	agg.parts[index] = data
	if len(agg.parts) != agg.total {
		return false, len(agg.parts), nil, nil
	}
	var out []byte
	for i := 0; i < agg.total; i++ {
		out = append(out, agg.parts[i]...)
	}
	return true, len(agg.parts), out, nil
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

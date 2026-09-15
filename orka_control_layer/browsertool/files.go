package browsertool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"mime"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/workspaceio"
)

type browserFiles struct {
	base     string
	identity connectors.GUIIdentity
}

// NewFiles binds publication to the authenticated invocation. Construction has
// no I/O; neither model arguments nor a different Session can select its root.
func NewFiles(baseStorage string, identity connectors.GUIIdentity) FileActions {
	return &browserFiles{base: baseStorage, identity: identity}
}

func (f *browserFiles) Execute(ctx context.Context, session Session, req Request) ([]FileResult, error) {
	if session.Identity != f.identity || f.identity.RunID == "" || session.Lease == nil {
		return nil, NewActionError("invalid_identity", "file action requires the authenticated operation session")
	}
	root, err := pathsafe.SessionRoot(f.base, f.identity.OwnerID, f.identity.ConversationID)
	if err != nil {
		return nil, NewActionError("invalid_identity", "file action requires a valid owner and conversation")
	}
	if err := ctx.Err(); err != nil {
		return nil, NewActionError("timeout", "file action canceled before publication")
	}
	if !fs.ValidPath(req.Path) || req.Path == "." || path.IsAbs(req.Path) || strings.ContainsAny(req.Path, "\\\x00") {
		return nil, NewActionError("invalid_argument", "path must be a canonical workspace-relative filename")
	}
	if req.Mode != "" && req.Mode != "create" && req.Mode != "replace" {
		return nil, NewActionError("invalid_argument", "file mode must be create or replace")
	}
	if _, err := pathsafe.Resolve(root, req.Path); err != nil {
		return nil, NewActionError("invalid_argument", "file path contains a symlink or escapes the workspace")
	}
	var data []byte
	mediaType := "image/png"
	switch req.Action {
	case "screenshot":
		data, err = capturePNG(ctx, session)
	case "download":
		data, mediaType, err = downloadBytes(ctx, session, req.URL)
	default:
		return nil, NewActionError("invalid_argument", "unsupported browser file action")
	}
	if err != nil {
		return nil, err
	}
	if _, err = pathsafe.EnsureSession(f.base, f.identity.OwnerID, f.identity.ConversationID); err != nil {
		return nil, NewActionError("file_error", "workspace unavailable")
	}
	_, err = workspaceio.PublishBinary(ctx, root, req.Path, req.Mode, bytes.NewReader(data), MaxFileBytes)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return nil, NewActionError("file_exists", "destination exists; use explicit replace to save history and overwrite")
		case errors.Is(err, workspaceio.ErrOutputLimit):
			return nil, NewActionError("output_limit", "file exceeds 16 MiB")
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return nil, NewActionError("timeout", "file action canceled before publication")
		default:
			return nil, NewActionError("file_error", "workspace publication refused")
		}
	}
	sum := sha256.Sum256(data)
	return []FileResult{{Path: req.Path, MIME: mediaType, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}, nil
}

func capturePNG(ctx context.Context, session Session) ([]byte, error) {
	var reply struct {
		Data string `json:"data"`
	}
	if err := session.Lease.Execute(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true, "captureBeyondViewport": false}, &reply); err != nil {
		return nil, err
	}
	if len(reply.Data) > base64.StdEncoding.EncodedLen(MaxFileBytes) {
		return nil, NewActionError("output_limit", "screenshot exceeds 16 MiB")
	}
	data, err := base64.StdEncoding.DecodeString(reply.Data)
	if err != nil || len(data) == 0 {
		return nil, NewActionError("invalid_image", "browser returned invalid PNG encoding")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, NewActionError("invalid_image", "browser returned invalid PNG")
	}
	// Bound decompression as well as encoded bytes before validating image CRCs.
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return nil, NewActionError("output_limit", "screenshot pixel count exceeds limit")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, NewActionError("invalid_image", fmt.Sprintf("browser returned corrupt PNG: %T", err))
	}
	return data, nil
}

// 205 data chunks fit 16 MiB with ample room for setup/cleanup within the lease's
// 256-command budget. Base64 plus JSON stays under the private 128 KiB reply cap.
const downloadChunkBytes = 80 << 10

type fileRuntimeReply struct {
	Result struct {
		ObjectID string          `json:"objectId"`
		Value    json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails json.RawMessage `json:"exceptionDetails"`
}

func downloadBytes(ctx context.Context, session Session, location string) ([]byte, string, error) {
	u, err := url.Parse(location)
	if err != nil || u.User != nil || len(location) > MaxExpressionBytes || session.ContextID <= 0 ||
		!((u.Scheme == "http" || u.Scheme == "https") && u.Host != "" || u.Scheme == "blob" && u.Opaque != "") {
		return nil, "", NewActionError("invalid_argument", "download requires HTTP(S) or a current-page blob URL and isolated context")
	}
	encoded, _ := json.Marshal(location)
	expression := "(" + downloadOpenScript + ")(" + string(encoded) + fmt.Sprintf(",%d)", MaxFileBytes)
	var opened fileRuntimeReply
	err = session.Lease.Execute(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "contextId": session.ContextID, "returnByValue": false, "awaitPromise": true, "allowUnsafeEvalBlockedByCSP": false}, &opened)
	if err != nil {
		return nil, "", err
	}
	if err = fileScriptError(opened); err != nil {
		return nil, "", err
	}
	if opened.Result.ObjectID == "" {
		return nil, "", NewActionError("script_error", "page fetch did not return a stream")
	}
	objectID := opened.Result.ObjectID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		var ignored fileRuntimeReply
		_ = session.Lease.Execute(cleanup, "Runtime.callFunctionOn", map[string]any{"objectId": objectID, "functionDeclaration": downloadCloseScript, "returnByValue": true, "awaitPromise": true}, &ignored)
		_ = session.Lease.Execute(cleanup, "Runtime.releaseObject", map[string]any{"objectId": objectID}, &struct{}{})
	}()
	data := make([]byte, 0, downloadChunkBytes)
	for commands := 0; commands < MaxFileBytes/downloadChunkBytes+2; commands++ {
		if err := ctx.Err(); err != nil {
			return nil, "", NewActionError("timeout", "page download canceled")
		}
		var reply fileRuntimeReply
		err = session.Lease.Execute(ctx, "Runtime.callFunctionOn", map[string]any{"objectId": objectID, "functionDeclaration": downloadReadScript, "arguments": []any{map[string]any{"value": downloadChunkBytes}}, "returnByValue": true, "awaitPromise": true}, &reply)
		if err != nil {
			return nil, "", err
		}
		if err = fileScriptError(reply); err != nil {
			return nil, "", err
		}
		var chunk struct {
			Data string `json:"data"`
			Done bool   `json:"done"`
			MIME string `json:"mime"`
		}
		if err := json.Unmarshal(reply.Result.Value, &chunk); err != nil {
			return nil, "", NewActionError("script_error", "invalid page download chunk")
		}
		if len(chunk.Data) > base64.StdEncoding.EncodedLen(downloadChunkBytes) {
			return nil, "", NewActionError("output_limit", "page download chunk exceeds limit")
		}
		raw, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			return nil, "", NewActionError("script_error", "invalid page download encoding")
		}
		if len(raw) > downloadChunkBytes {
			return nil, "", NewActionError("output_limit", "page download chunk exceeds limit")
		}
		if len(data)+len(raw) > MaxFileBytes {
			return nil, "", NewActionError("output_limit", "download exceeds 16 MiB")
		}
		data = append(data, raw...)
		if chunk.Done {
			mediaType, _, err := mime.ParseMediaType(chunk.MIME)
			if err != nil || len(mediaType) > 256 || !strings.Contains(mediaType, "/") {
				mediaType = "application/octet-stream"
			}
			return data, mediaType, nil
		}
		if len(raw) == 0 {
			return nil, "", NewActionError("script_error", "page download made no progress")
		}
	}
	return nil, "", NewActionError("output_limit", "download exceeded operation command budget")
}

func fileScriptError(reply fileRuntimeReply) error {
	if len(reply.ExceptionDetails) == 0 || string(reply.ExceptionDetails) == "null" {
		return nil
	}
	// Only emit our fixed diagnostics, never browser exception stacks, credentialed
	// URLs, response bodies or page data in an error message.
	raw := string(reply.ExceptionDetails)
	if strings.Contains(raw, "ORKA_OUTPUT_LIMIT") {
		return NewActionError("output_limit", "download exceeds 16 MiB")
	}
	if strings.Contains(raw, "ORKA_BLOB_ORIGIN") {
		return NewActionError("invalid_argument", "blob must belong to the current page origin")
	}
	for status := 100; status <= 599; status++ {
		if strings.Contains(raw, fmt.Sprintf("ORKA_HTTP_%d", status)) {
			return NewActionError("script_error", fmt.Sprintf("page download returned HTTP %d", status))
		}
	}
	return NewActionError("script_error", "page fetch failed (HTTP/CORS/CSP/network or detached page); no file published")
}

// Fixed helpers run only in the leased page's isolated world. The page browser
// owns cookies and response credentials; Go never receives headers or cookies.
const downloadOpenScript = `async function orkaDownloadOpen(location, limit) {
 const url = new URL(location);
 if (url.protocol === 'blob:' && url.origin !== globalThis.location.origin) throw new Error('ORKA_BLOB_ORIGIN');
 if (!['http:', 'https:', 'blob:'].includes(url.protocol)) throw new Error('ORKA_BAD_URL');
 const abort = new AbortController();
 try {
  const response = await fetch(url.href, {credentials:'include', mode:'cors', redirect:'follow', signal:abort.signal});
  if (!response.ok) throw new Error('ORKA_HTTP_' + response.status);
  const length = Number(response.headers.get('content-length'));
  if (Number.isFinite(length) && length > limit) throw new Error('ORKA_OUTPUT_LIMIT');
  if (!response.body) throw new Error('ORKA_NO_BODY');
  return {reader:response.body.getReader(), abort, limit, received:0, pending:null, offset:0, done:false,
          mime:(response.headers.get('content-type') || 'application/octet-stream').slice(0,256)};
 } catch (error) { abort.abort(); throw error; }
}`

const downloadReadScript = `async function orkaDownloadRead(chunkLimit) {
 const pieces=[]; let size=0;
 try {
  while (size < chunkLimit && !this.done) {
   if (!this.pending || this.offset >= this.pending.length) {
    const next = await this.reader.read();
    if (next.done) {this.done=true; break;}
    this.received += next.value.byteLength;
    if (this.received > this.limit) throw new Error('ORKA_OUTPUT_LIMIT');
    this.pending=next.value; this.offset=0;
   }
   const length=Math.min(chunkLimit-size,this.pending.length-this.offset);
   if (length) {pieces.push(this.pending.subarray(this.offset,this.offset+length)); this.offset+=length; size+=length;}
  }
  let text='';
  for (const part of pieces) for(let i=0;i<part.length;i+=8192) text+=String.fromCharCode(...part.subarray(i,i+8192));
  return {data:btoa(text),done:this.done,mime:this.mime};
 } catch(error) {this.abort.abort(); try {await this.reader.cancel();} catch (_) {} throw error;}
}`
const downloadCloseScript = `async function orkaDownloadClose() {
 this.abort.abort(); try {await this.reader.cancel();} catch (_) {} this.pending=null; return true;
}`

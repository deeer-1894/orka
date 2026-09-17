package browsertool

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/orka-oss/orka_control_layer/connectors"
	"github.com/orka-oss/orka_core/pathsafe"
)

const maxPreviewBytes = 1 << 20

// PreviewReader is an optional capability of conversation-scoped file storage.
// Reading and rendering stay separate: the engine owns the existing page lease.
type PreviewReader interface {
	ReadPreview(context.Context, connectors.GUIIdentity, string) ([]byte, FileResult, error)
}

func validPreviewPath(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return fs.ValidPath(name) && !strings.ContainsAny(name, "\\:\x00") && (ext == ".html" || ext == ".htm")
}

func (f *browserFiles) ReadPreview(ctx context.Context, identity connectors.GUIIdentity, name string) ([]byte, FileResult, error) {
	fail := func(code, message string) ([]byte, FileResult, error) {
		return nil, FileResult{}, NewActionError(code, message)
	}
	if err := ctx.Err(); err != nil {
		return nil, FileResult{}, err
	}
	if identity != f.identity {
		return fail("invalid_identity", "preview storage does not belong to this operation")
	}
	if !validPreviewPath(name) {
		return fail("invalid_argument", "preview requires a canonical workspace-relative .html or .htm file")
	}
	root, err := pathsafe.SessionRoot(f.base, f.identity.OwnerID, f.identity.ConversationID)
	if err != nil || f.identity.RunID == "" {
		return fail("invalid_identity", "preview requires an authenticated conversation workspace")
	}
	if _, err = pathsafe.Resolve(root, name); err != nil {
		return fail("invalid_argument", "preview path escapes the workspace or contains a symlink")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return fail("file_error", "conversation workspace is unavailable")
	}
	defer dir.Close()
	// Nonblocking open avoids hanging on a FIFO swapped in before fstat.
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fail("file_error", "HTML file is unavailable in this conversation; check its workspace-relative path")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fail("file_error", "preview requires a regular HTML file")
	}
	if info.Size() > maxPreviewBytes {
		return fail("output_limit", "preview supports self-contained HTML up to 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPreviewBytes+1))
	if err != nil {
		return fail("file_error", "HTML file could not be read completely")
	}
	if len(data) > maxPreviewBytes {
		return fail("output_limit", "preview supports self-contained HTML up to 1 MiB")
	}
	if len(data) == 0 || !utf8.Valid(data) {
		return fail("invalid_document", "preview requires nonempty UTF-8 HTML")
	}
	if err = ctx.Err(); err != nil {
		return nil, FileResult{}, err
	}
	sum := sha256.Sum256(data)
	return data, FileResult{Path: name, MIME: "text/html", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func openPreview(ctx context.Context, session Session, reader PreviewReader, name string) (*FileResult, error) {
	data, receipt, err := reader.ReadPreview(ctx, session.Identity, name)
	if err != nil {
		return nil, err
	}
	// Bytes cross the private transport, never the model context. The browser
	// service supplies an opaque origin and an offline document policy.
	err = session.Lease.Execute(ctx, "Orka.previewHTML", map[string]any{"html_base64": base64.StdEncoding.EncodeToString(data)}, &struct{}{})
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

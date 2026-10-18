// Package delivery stores fixed, verified delivery snapshots outside writable
// session workspaces. Only the control process should call Publish. Authorization
// belongs to its caller; owner/conversation/run identify the storage boundary.
package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/orka-oss/orka_core/pathsafe"
)

var ErrExists = errors.New("delivery already exists")
var ErrNotFound = errors.New("delivery not found")
var ErrIntegrity = errors.New("delivery integrity check failed")

const StoreDir = ".orka_deliveries"
const manifestVersion = "orka.delivery/v1"
const MaxFiles = 256
const MaxFileBytes int64 = 256 << 20
const MaxTotalBytes int64 = 1 << 30

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Version        string    `json:"version"`
	ConversationID string    `json:"conversation_id"`
	RunID          string    `json:"run_id"`
	CreatedAt      time.Time `json:"created_at"`
	Files          []File    `json:"files"`
}

// Publish copies declared regular files, hashes copied bytes, and commits a
// manifest last. Mkdir reserves the run atomically across control processes.
// Failures remove the incomplete run; published run IDs can never be replaced.
func Publish(base, owner, conv, run string, paths []string) (Manifest, error) {
	return publish(base, owner, conv, run, paths, nil)
}

// PublishChecked validates the copied snapshot before committing its manifest.
// The callback sees only declared files, through a read-only filesystem whose
// lifetime ends when the callback returns. A failed check removes the run and
// preserves the callback error for errors.Is. A non-nil validator is required.
func PublishChecked(base, owner, conv, run string, paths []string, validate func(fs.FS) error) (Manifest, error) {
	if validate == nil {
		return Manifest{}, fmt.Errorf("snapshot validator is required")
	}
	return publish(base, owner, conv, run, paths, validate)
}

func publish(base, owner, conv, run string, paths []string, validate func(fs.FS) error) (Manifest, error) {
	if !validID(run) {
		return Manifest{}, fmt.Errorf("invalid run ID")
	}
	if len(paths) == 0 || len(paths) > MaxFiles {
		return Manifest{}, fmt.Errorf("declare 1..%d files", MaxFiles)
	}
	declared := append([]string(nil), paths...)
	sort.Strings(declared)
	for i, p := range declared {
		if !validPath(p) || (i > 0 && declared[i-1] == p) {
			return Manifest{}, fmt.Errorf("invalid or duplicate declared path")
		}
	}
	session, err := pathsafe.SessionRoot(base, owner, conv)
	if err != nil {
		return Manifest{}, err
	}
	source, err := os.OpenRoot(session)
	if err != nil {
		return Manifest{}, err
	}
	defer source.Close()
	store, err := openStore(base, owner, conv, true)
	if err != nil {
		return Manifest{}, err
	}
	defer store.Close()
	if err := store.Mkdir(run, 0700); err != nil {
		if os.IsExist(err) {
			return Manifest{}, ErrExists
		}
		return Manifest{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = store.RemoveAll(run)
		}
	}()
	manifest := Manifest{Version: manifestVersion, ConversationID: conv, RunID: run, CreatedAt: time.Now().UTC(), Files: make([]File, 0, len(declared))}
	total := int64(0)
	for _, p := range declared {
		if _, err := pathsafe.Resolve(session, p); err != nil {
			return Manifest{}, err
		}
		file, err := copyFile(source, store, p, path.Join(run, "files", p))
		if err != nil {
			return Manifest{}, fmt.Errorf("snapshot %q: %w", p, err)
		}
		total += file.Size
		if total > MaxTotalBytes {
			return Manifest{}, fmt.Errorf("delivery exceeds total size limit")
		}
		manifest.Files = append(manifest.Files, file)
	}
	if validate != nil {
		snapshot, err := store.OpenRoot(path.Join(run, "files"))
		if err != nil {
			return Manifest{}, err
		}
		err = func() error { defer snapshot.Close(); return validate(snapshot.FS()) }()
		if err != nil {
			return Manifest{}, fmt.Errorf("snapshot validation: %w", err)
		}
	}
	if err := commitManifest(store, run, manifest); err != nil {
		return Manifest{}, err
	}
	committed = true
	return manifest, nil
}

// Read checks the saved size and SHA-256 before returning a declared file.
// It never reads the live session and never creates missing storage directories.
func Read(base, owner, conv, run, rel string) ([]byte, error) {
	if !validID(run) || !validPath(rel) {
		return nil, fmt.Errorf("invalid delivery path or run ID")
	}
	store, err := openStore(base, owner, conv, false)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	manifest, err := loadManifest(store, conv, run)
	if err != nil {
		return nil, err
	}
	for _, file := range manifest.Files {
		if file.Path != rel {
			continue
		}
		data, err := readBounded(store, path.Join(run, "files", rel), MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: snapshot file unavailable", ErrIntegrity)
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != file.Size || hex.EncodeToString(sum[:]) != file.SHA256 {
			return nil, ErrIntegrity
		}
		return data, nil
	}
	return nil, ErrNotFound
}

// List returns committed manifests for this owner/conversation, newest first.
// Incomplete runs (no committed manifest) are hidden. A malformed committed
// manifest is an integrity error rather than a silently omitted delivery.
func List(base, owner, conv string) ([]Manifest, error) {
	store, err := openStore(base, owner, conv, false)
	if errors.Is(err, ErrNotFound) {
		return []Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer store.Close()
	dir, err := store.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	manifests := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		m, err := loadManifest(store, conv, entry.Name())
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, m)
	}
	sort.Slice(manifests, func(i, j int) bool {
		if manifests[i].CreatedAt.Equal(manifests[j].CreatedAt) {
			return manifests[i].RunID < manifests[j].RunID
		}
		return manifests[i].CreatedAt.After(manifests[j].CreatedAt)
	})
	return manifests, nil
}

func openStore(base, owner, conv string, create bool) (*os.Root, error) {
	if _, err := pathsafe.SessionRoot(base, owner, conv); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(owner))
	location, err := pathsafe.Resolve(base, filepath.Join(StoreDir, hex.EncodeToString(sum[:]), conv))
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(location, 0700); err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(location)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return root, err
}
func validID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validPath(p string) bool {
	return p != "" && p != "." && len(p) <= 4096 && !strings.ContainsAny(p, "\\\x00") && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

func copyFile(source, store *os.Root, rel, dst string) (File, error) {
	info, err := source.Lstat(rel)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return File{}, fmt.Errorf("source must be a regular file within size limit")
	}
	input, err := openSource(source, rel)
	if err != nil {
		return File{}, err
	}
	defer input.Close()
	before, err := input.Stat()
	if err != nil {
		return File{}, err
	}
	if !before.Mode().IsRegular() || !os.SameFile(info, before) {
		return File{}, fmt.Errorf("source changed during open")
	}
	if err := store.MkdirAll(path.Dir(dst), 0700); err != nil {
		return File{}, err
	}
	output, err := store.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return File{}, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, MaxFileBytes+1))
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return File{}, copyErr
	}
	if syncErr != nil {
		return File{}, syncErr
	}
	if closeErr != nil {
		return File{}, closeErr
	}
	after, err := input.Stat()
	if err != nil {
		return File{}, err
	}
	current, err := source.Lstat(rel)
	if err != nil {
		return File{}, err
	}
	if n > MaxFileBytes || n != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, current) {
		return File{}, fmt.Errorf("source changed while publishing")
	}
	return File{Path: rel, Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

package pathsafe

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WorkspaceDirMode and WorkspaceFileMode preserve named-user write permissions
// inherited from a shared host/container default ACL. Without a default ACL,
// the process umask still applies. No ACL or permission on existing files changes.
const WorkspaceDirMode os.FileMode = 0775
const WorkspaceFileMode os.FileMode = 0664

// CopySession snapshots regular files into a new, independent conversation.
// Symlinks and special files are skipped, never dereferenced. File handles are
// rooted at the two sessions to enforce containment even during concurrent edits.
func CopySession(base, sourceOwner, sourceID, targetOwner, targetID string) ([]string, error) {
	source, err := EnsureSession(base, sourceOwner, sourceID)
	if err != nil {
		return nil, err
	}
	target, err := SessionRoot(base, targetOwner, targetID)
	if err != nil {
		return nil, err
	}
	if source == target {
		return nil, fmt.Errorf("cannot copy a session onto itself")
	}
	if _, err := os.Stat(target); err == nil {
		return nil, fmt.Errorf("target workspace already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	target, err = EnsureSession(base, targetOwner, targetID)
	if err != nil {
		return nil, err
	}
	src, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	dst, err := os.OpenRoot(target)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	var skipped []string
	err = fs.WalkDir(src.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			skipped = append(skipped, p)
			return nil
		}
		if d.IsDir() {
			return dst.MkdirAll(p, WorkspaceDirMode)
		}
		if !d.Type().IsRegular() {
			skipped = append(skipped, p)
			return nil
		}
		in, err := src.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		st, err := in.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("source changed type: %s", p)
		}
		if err := dst.MkdirAll(filepath.Dir(p), WorkspaceDirMode); err != nil {
			return err
		}
		out, err := dst.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, WorkspaceFileMode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		_ = os.RemoveAll(target)
	} // only the newly-created target, never source
	return skipped, err
}

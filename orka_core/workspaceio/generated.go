package workspaceio

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/orka-oss/orka_core/pathsafe"
)

// ReplaceGenerated owns a temporary workspace output and publishes it only after
// generation succeeds. Unlike the legacy text-write backup contract, replacement
// of an existing generated artifact requires a successful prior-version backup.
// The callback receives a workspace-relative path with the target's extension.
func ReplaceGenerated(rootPath, target string, generate func(string) error) (WriteResult, error) {
	return publishGenerated(context.Background(), rootPath, NormalizePath(target), "replace", func(_ *os.Root, temp string) error { return generate(temp) })
}

var ErrOutputLimit = errors.New("binary output exceeds size limit")
var publicationLocks [64]sync.Mutex

// PublishBinary stages bounded bytes before publishing the complete file.
// Empty mode means create; existing files are only replaced with explicit
// replace and a successful prior-version backup. Relative paths are strict:
// unlike legacy text adapters, absolute paths are never reduced to basenames.
// Reader cancellation is checked between reads; a blocking reader must itself
// honor cancellation. Browser callers pass bounded in-memory bytes.
func PublishBinary(ctx context.Context, rootPath, target, mode string, src io.Reader, maxBytes int64) (WriteResult, error) {
	if mode == "" {
		mode = "create"
	}
	if mode != "create" && mode != "replace" {
		return WriteResult{}, fmt.Errorf("mode must be create or replace")
	}
	if !fs.ValidPath(target) || target == "." || strings.ContainsAny(target, "\\\x00") || path.IsAbs(target) {
		return WriteResult{}, pathsafe.ErrEscapes
	}
	if src == nil || maxBytes <= 0 {
		return WriteResult{}, fmt.Errorf("reader and positive byte limit required")
	}
	return publishGenerated(ctx, rootPath, target, mode, func(root *os.Root, temp string) error {
		f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, pathsafe.WorkspaceFileMode)
		if err != nil {
			return err
		}
		defer f.Close()
		var count int64
		buffer := make([]byte, 32<<10)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := src.Read(buffer)
			if n > 0 {
				if int64(n) > maxBytes-count {
					return ErrOutputLimit
				}
				count += int64(n)
				if _, writeErr := f.Write(buffer[:n]); writeErr != nil {
					return writeErr
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return f.Sync()
	})
}

func publishGenerated(ctx context.Context, rootPath, target, mode string, generate func(*os.Root, string) error) (WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}
	if target == "" || target == "." {
		return WriteResult{}, fmt.Errorf("output path required")
	}
	if _, err := pathsafe.Resolve(rootPath, target); err != nil {
		return WriteResult{}, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return WriteResult{}, err
	}
	defer root.Close()
	temporaryDir := ".orka-generate-" + rand.Text()
	if err := root.Mkdir(temporaryDir, 0700); err != nil {
		return WriteResult{}, err
	}
	defer root.RemoveAll(temporaryDir)
	temp := filepath.Join(temporaryDir, filepath.Base(target))
	if err := generate(root, filepath.ToSlash(temp)); err != nil {
		return WriteResult{}, err
	}
	generated, err := root.Lstat(temp)
	if err != nil {
		return WriteResult{}, err
	}
	if !generated.Mode().IsRegular() {
		return WriteResult{}, fmt.Errorf("generator did not produce a regular file")
	}
	result := WriteResult{Mode: mode, Path: target, Bytes: int(generated.Size())}
	// Serialize cooperative replacement publishers in this process so each
	// displaced version is saved. Atomic create also works across processes.
	key := fnv.New32a()
	_, _ = key.Write([]byte(filepath.Clean(rootPath) + "\x00" + target))
	lock := &publicationLocks[key.Sum32()%uint32(len(publicationLocks))]
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := pathsafe.Resolve(rootPath, target); err != nil {
		return result, err
	}
	if err := root.MkdirAll(filepath.Dir(target), pathsafe.WorkspaceDirMode); err != nil {
		return result, err
	}
	if mode == "create" {
		// Linking a fully written temporary inode is atomic and refuses existing
		// files. Unlike O_EXCL+copy, readers never see a partially copied target.
		if err := root.Link(temp, target); err != nil {
			return result, err
		}
		return result, nil
	}
	oldInfo, err := root.Lstat(target)
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	if err == nil {
		if !oldInfo.Mode().IsRegular() {
			return result, fmt.Errorf("output is not a regular file; replacement refused")
		}
		old, err := root.ReadFile(target)
		if err != nil {
			return result, err
		}
		result.Version, err = Snapshot(root, target, old)
		if err != nil || result.Version == "" {
			return result, fmt.Errorf("previous version could not be saved; replacement refused")
		}
	}
	if err := root.MkdirAll(filepath.Dir(target), pathsafe.WorkspaceDirMode); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := root.Rename(temp, target); err != nil {
		return result, err
	}
	return result, nil
}

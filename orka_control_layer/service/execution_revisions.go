package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

// These are file versions at invocation time, either declared outputs or a
// bounded workspace sample. They do not claim a command read or verified every
// file. Keep this separate from process status and business acceptance.
type ExecutionRevision struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func executionRevisions(ctx context.Context) (files []ExecutionRevision, partial bool) {
	d := deliveryFrom(ctx)
	if d == nil {
		return nil, false
	}
	paths := d.snapshot()
	if len(paths) == 0 {
		var partial bool
		paths, partial = executionWorkspacePaths(ctx, d.root)
		files, incomplete := hashExecutionPaths(ctx, d.root, paths)
		return files, partial || incomplete
	}
	return hashExecutionPaths(ctx, d.root, paths)
}

func hashExecutionPaths(ctx context.Context, directory string, paths []string) (files []ExecutionRevision, partial bool) {
	if len(paths) == 0 {
		return nil, false
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, true
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	remaining := int64(8 << 20)
	for _, path := range paths {
		if ctx.Err() != nil || remaining <= 0 || len(files) >= 64 {
			return files, true
		}
		st, err := root.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !st.Mode().IsRegular() || st.Size() > remaining {
			partial = true
			continue
		}
		f, err := root.Open(path)
		if err != nil {
			partial = true
			continue
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(f, remaining+1))
		f.Close()
		if err != nil || n > remaining {
			partial = true
			continue
		}
		remaining -= n
		files = append(files, ExecutionRevision{Path: path, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	return files, partial
}

func changedRevisions(before, after []ExecutionRevision) []string {
	byPath := make(map[string]string, len(after))
	for _, file := range after {
		byPath[file.Path] = file.SHA256
	}
	var changed []string
	for _, file := range before {
		if byPath[file.Path] != file.SHA256 {
			changed = append(changed, file.Path)
		}
	}
	return changed
}

// Refresh the union once, so an API read never rehashes a large archive for
// every historical invocation. Bounds and partial status remain explicit.
func refreshExecutionHistory(ctx context.Context, root string, records []ExecutionEvidence) {
	if len(records) == 0 {
		return
	}
	var paths []string
	seen := map[string]bool{}
	for _, record := range records {
		for _, file := range record.Files {
			if seen[file.Path] {
				continue
			}
			seen[file.Path] = true
			paths = append(paths, file.Path)
		}
	}
	current, incomplete := hashExecutionPaths(ctx, root, paths)
	for i := range records {
		records[i].RevisionsPartial = records[i].RevisionsPartial || incomplete
		records[i].ChangedSince = changedRevisions(records[i].Files, current)
	}
}

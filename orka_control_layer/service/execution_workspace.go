package service

import (
	"context"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Workspace sampling fills provenance gaps without inventing delivery outputs.
// Read directory entries in batches: even a huge dependency directory must not
// cause an unbounded allocation. Never traverse symlinks or hidden directories.
func executionWorkspacePaths(ctx context.Context, directory string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, true
	}
	defer root.Close()
	queue := []string{"."}
	var paths []string
	partial, visited := false, 0
	for len(queue) > 0 && visited < 4096 && ctx.Err() == nil {
		dir := queue[0]
		queue = queue[1:]
		f, err := root.Open(dir)
		if err != nil {
			partial = true
			continue
		}
		for visited < 4096 && ctx.Err() == nil {
			entries, err := f.ReadDir(min(64, 4096-visited))
			for _, entry := range entries {
				visited++
				name := entry.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" {
					continue
				}
				p := path.Join(dir, name)
				if entry.IsDir() {
					queue = append(queue, p)
				} else if entry.Type().IsRegular() {
					paths = append(paths, p)
				}
			}
			if err != nil {
				if err != io.EOF {
					partial = true
				}
				break
			}
		}
		f.Close()
	}
	partial = partial || visited >= 4096 || ctx.Err() != nil
	// Prefer root deliverables over nested temporary copies, deterministically.
	sort.Slice(paths, func(i, j int) bool {
		a, b := strings.Count(paths[i], "/"), strings.Count(paths[j], "/")
		if a != b {
			return a < b
		}
		return paths[i] < paths[j]
	})
	if len(paths) > 64 {
		paths = paths[:64]
		partial = true
	}
	return paths, partial
}

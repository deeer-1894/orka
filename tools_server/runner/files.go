package runner

import (
	"context"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// FileChanges is an observation of workspace metadata, not an acceptance check.
// Scans never follow symlinks, read file contents or enumerate outside the root.
type FileChanges struct {
	Paths   []string `json:"paths"`
	Partial bool     `json:"partial"`
}

type fileStamp struct{ size, modified int64 }
type fileInventory struct {
	files   map[string]fileStamp
	partial bool
}

const inventoryLimit = 4096
const changeLimit = 256

func inventory(ctx context.Context, root *os.Root) fileInventory {
	s := fileInventory{files: make(map[string]fileStamp)}
	visited := 0
	_ = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			s.partial = true
			return nil
		}
		visited++
		if ctx.Err() != nil || visited > inventoryLimit {
			s.partial = true
			return fs.SkipAll
		}
		base := path.Base(name)
		if name != "." && (strings.HasPrefix(base, ".") || base == "__pycache__" || base == "node_modules") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		st, err := entry.Info()
		if err != nil {
			s.partial = true
			return nil
		}
		s.files[name] = fileStamp{st.Size(), st.ModTime().UnixNano()}
		return nil
	})
	return s
}

func changedFiles(before, after fileInventory) *FileChanges {
	c := &FileChanges{Paths: []string{}, Partial: before.partial || after.partial}
	for name, stamp := range after.files {
		if old, ok := before.files[name]; !ok || old != stamp {
			c.Paths = append(c.Paths, name)
		}
	}
	sort.Strings(c.Paths)
	if len(c.Paths) > changeLimit {
		c.Paths = c.Paths[:changeLimit]
		c.Partial = true
	}
	return c
}

package artifacts

import (
	"context"
	"fmt"
	"github.com/orka-oss/orka_core/pathsafe"
	"io/fs"
	"os"
	"sort"
)

// CheckDeclared previews the namespace a fixed delivery will contain. Evidence
// present only in the live workspace cannot make an incomplete delivery pass.
// Publication still rechecks the copied bytes to protect against later edits.
func CheckDeclared(ctx context.Context, rootPath string, paths []string) Report {
	if len(paths) == 0 || len(paths) > MaxFiles {
		return CheckFS(ctx, nil, paths)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Report{Files: []File{}, Failures: []string{"workspace: " + err.Error()}}
	}
	defer root.Close()
	selected := &declaredFS{root: root, rootPath: rootPath, allowed: make(map[string]bool, len(paths)), missing: make(map[string]bool)}
	for _, p := range paths {
		if ValidPath(p) {
			selected.allowed[p] = true
		}
	}
	report := CheckFS(ctx, selected, paths)
	missing := make([]string, 0, len(selected.missing))
	for p := range selected.missing {
		missing = append(missing, p)
	}
	sort.Strings(missing)
	for _, p := range missing {
		report.OK = false
		report.Failures = append(report.Failures, fmt.Sprintf("dependency %q is not declared; add it to update_plan.outputs before checking delivery", p))
	}
	return report
}

// Each checker runs synchronously on this per-call view; no shared state or
// workspace mutations are needed to model the published file namespace.
type declaredFS struct {
	root     *os.Root
	rootPath string
	allowed  map[string]bool
	missing  map[string]bool
}

func (d *declaredFS) Stat(name string) (fs.FileInfo, error) {
	if !d.allowed[name] {
		d.missing[name] = true
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	if _, err := pathsafe.Resolve(d.rootPath, name); err != nil {
		return nil, err
	}
	info, err := d.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("declared output must be a regular file")
	}
	return info, nil
}
func (d *declaredFS) Open(name string) (fs.File, error) {
	before, err := d.Stat(name)
	if err != nil {
		return nil, err
	}
	f, err := openDeclared(d.root, name)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		f.Close()
		return nil, fmt.Errorf("declared output changed during open")
	}
	return f, nil
}

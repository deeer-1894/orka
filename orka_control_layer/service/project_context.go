package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/pathsafe"
	"github.com/orka-oss/orka_core/workspaceio"
)

// A conversation filed under a project reads the project's standing
// instructions and works with its shared files. The files live once, in the
// project's area; each run gets them as a mirror under project/ in its own
// workspace (so every tool, local or sandboxed, sees them as plain files), and
// whatever the run wrote under project/ is published back as a new version
// when it ends. Writing anywhere else stays private to the conversation, so a
// project file only changes when a run names it on purpose.

const (
	projectMirrorDir      = "project"
	maxProjectFiles       = 500
	maxProjectFileBytes   = 20 << 20
	maxProjectListedFiles = 50
)

type projectFile struct {
	Rel  string
	Size int64
}

// runProject is the project a run belongs to, with its two file roots.
type runProject struct {
	*db.Project
	sessionRoot, projectRoot string
	files                    []projectFile
}

// openRunProject loads the run's project and mirrors its files in. nil when
// the conversation belongs to no project or anything about it is unavailable:
// a run never fails because its project could not be read.
func (s *ChatService) openRunProject(ctx context.Context, req ChatRunRequest) *runProject {
	if s.Msg == nil || s.Msg.Store == nil || s.Msg.Store.Conversations == nil || s.Msg.Store.Projects == nil || req.ConversationID == "" {
		return nil
	}
	conv, err := s.Msg.Store.GetConversation(ctx, req.ConversationID)
	if err != nil || conv.ProjectID == "" {
		return nil
	}
	project, err := s.Msg.Store.GetProject(ctx, conv.OwnerEmail, conv.ProjectID)
	if err != nil {
		return nil
	}
	base := s.Cfg.Storage.BaseStoragePath
	sessionRoot, err := pathsafe.EnsureSession(base, conv.OwnerEmail, conv.ConversationID)
	if err != nil {
		return nil
	}
	projectRoot, err := pathsafe.EnsureProject(base, conv.OwnerEmail, project.ProjectID)
	if err != nil {
		return nil
	}
	rp := &runProject{Project: project, sessionRoot: sessionRoot, projectRoot: projectRoot}
	files, err := mirrorProjectIn(projectRoot, sessionRoot)
	if err != nil && s.Log != nil {
		s.Log.Warn("project files not mirrored", "project_id", project.ProjectID, "err", err)
	}
	rp.files = files
	return rp
}

// prompt is the block appended to the system prompt.
func (p *runProject) prompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[项目：%s]\n", p.Name)
	if p.Instructions != "" {
		b.WriteString("项目指令（本项目内每次运行都要遵守）：\n" + p.Instructions + "\n")
	}
	if len(p.files) == 0 {
		b.WriteString("项目共享文件：暂无。需要新增项目文件时写到 project/ 目录下。\n")
	} else {
		fmt.Fprintf(&b, "项目共享文件在工作区的 %s/ 目录（共 %d 个）：\n", projectMirrorDir, len(p.files))
		for i, f := range p.files {
			if i == maxProjectListedFiles {
				fmt.Fprintf(&b, "- ……另有 %d 个\n", len(p.files)-i)
				break
			}
			fmt.Fprintf(&b, "- %s/%s（%s）\n", projectMirrorDir, f.Rel, sizeLabel(f.Size))
		}
	}
	b.WriteString("只有写到 project/ 下的文件会在运行结束后保存为项目文件（保留旧版本）；其他文件只属于本对话。")
	return b.String()
}

// publish copies what the run changed under project/ back to the project.
func (p *runProject) publish() ([]string, error) {
	if p == nil {
		return nil, nil
	}
	return publishProjectOut(p.sessionRoot, p.projectRoot)
}

func sizeLabel(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// regularFiles lists the regular files under root (no symlinks, no version
// trash), sorted, bounded.
func regularFiles(root *os.Root) ([]projectFile, error) {
	var out []projectFile
	err := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if rel == workspaceio.TrashDir {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxProjectFileBytes {
			return nil
		}
		if len(out) == maxProjectFiles {
			return fs.SkipAll
		}
		out = append(out, projectFile{Rel: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, err
}

func mirrorProjectIn(projectRoot, sessionRoot string) ([]projectFile, error) {
	src, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	session, err := os.OpenRoot(sessionRoot)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if err := session.MkdirAll(projectMirrorDir, pathsafe.WorkspaceDirMode); err != nil {
		return nil, err
	}
	files, err := regularFiles(src)
	if err != nil {
		return files, err
	}
	var errs []error
	// The mirror starts each run equal to the project: a file deleted from the
	// project must not linger here and be published back when the run ends.
	if mirror, err := session.OpenRoot(projectMirrorDir); err == nil {
		inProject := make(map[string]bool, len(files))
		for _, f := range files {
			inProject[f.Rel] = true
		}
		stale, _ := regularFiles(mirror)
		for _, f := range stale {
			if !inProject[f.Rel] {
				if err := mirror.Remove(filepath.FromSlash(f.Rel)); err != nil {
					errs = append(errs, err)
				}
			}
		}
		mirror.Close()
	}
	for _, f := range files {
		data, err := src.ReadFile(f.Rel)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		dst := filepath.Join(projectMirrorDir, filepath.FromSlash(f.Rel))
		if current, err := session.ReadFile(dst); err == nil && bytes.Equal(current, data) {
			continue
		}
		if err := session.MkdirAll(filepath.Dir(dst), pathsafe.WorkspaceDirMode); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := session.WriteFile(dst, data, pathsafe.WorkspaceFileMode); err != nil {
			errs = append(errs, err)
		}
	}
	return files, errors.Join(errs...)
}

func publishProjectOut(sessionRoot, projectRoot string) ([]string, error) {
	session, err := os.OpenRoot(sessionRoot)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	mirror, err := session.OpenRoot(projectMirrorDir)
	if err != nil {
		return nil, nil // nothing mirrored, nothing to publish
	}
	defer mirror.Close()
	dst, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	files, err := regularFiles(mirror)
	if err != nil {
		return nil, err
	}
	var changed []string
	var errs []error
	for _, f := range files {
		data, err := mirror.ReadFile(f.Rel)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rel := filepath.FromSlash(f.Rel)
		old, readErr := dst.ReadFile(rel)
		if readErr == nil && bytes.Equal(old, data) {
			continue
		}
		if readErr == nil {
			if _, err := workspaceio.Snapshot(dst, rel, old); err != nil {
				errs = append(errs, fmt.Errorf("keep previous version of %s: %w", f.Rel, err))
				continue // never overwrite a project file without its history
			}
		}
		if err := dst.MkdirAll(filepath.Dir(rel), pathsafe.WorkspaceDirMode); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := dst.WriteFile(rel, data, pathsafe.WorkspaceFileMode); err != nil {
			errs = append(errs, err)
			continue
		}
		changed = append(changed, f.Rel)
	}
	return changed, errors.Join(errs...)
}

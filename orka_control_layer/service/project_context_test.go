package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/db"
	"github.com/orka-oss/orka_core/workspaceio"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProjectFilesMirrorInAndOnlyProjectWritesPublishBackWithHistory(t *testing.T) {
	project, session := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(project, "brief.md"), "v1")
	writeFile(t, filepath.Join(project, "data", "list.csv"), "a,b")
	if err := os.Symlink("/etc/passwd", filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}

	files, err := mirrorProjectIn(project, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Rel != "brief.md" || files[1].Rel != "data/list.csv" {
		t.Fatalf("mirrored %+v, want the two regular files", files)
	}
	if readFile(t, filepath.Join(session, "project", "brief.md")) != "v1" {
		t.Fatal("project file not mirrored into the session")
	}
	if _, err := os.Lstat(filepath.Join(session, "project", "escape")); !os.IsNotExist(err) {
		t.Fatal("symlinks must never be mirrored")
	}

	// The run edits a project file, adds one, and writes a private file.
	writeFile(t, filepath.Join(session, "project", "brief.md"), "v2")
	writeFile(t, filepath.Join(session, "project", "notes.md"), "new")
	writeFile(t, filepath.Join(session, "private.md"), "mine")
	changed, err := publishProjectOut(session, project)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "brief.md,notes.md" {
		t.Fatalf("published %v, want brief.md and notes.md", changed)
	}
	if readFile(t, filepath.Join(project, "brief.md")) != "v2" || readFile(t, filepath.Join(project, "notes.md")) != "new" {
		t.Fatal("project files not updated")
	}
	if _, err := os.Stat(filepath.Join(project, "private.md")); !os.IsNotExist(err) {
		t.Fatal("a file outside project/ must stay private to the conversation")
	}
	stamps, _ := os.ReadDir(filepath.Join(project, workspaceio.TrashDir))
	if len(stamps) != 1 || readFile(t, filepath.Join(project, workspaceio.TrashDir, stamps[0].Name(), "brief.md")) != "v1" {
		t.Fatal("the overwritten project file must keep its previous version")
	}
	if again, _ := publishProjectOut(session, project); len(again) != 0 {
		t.Fatalf("an unchanged mirror publishes nothing, got %v", again)
	}

	// A file deleted from the project leaves the mirror on the next run, so it
	// cannot be published back.
	if err := os.Remove(filepath.Join(project, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := mirrorProjectIn(project, session); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(session, "project", "notes.md")); !os.IsNotExist(err) {
		t.Fatal("a deleted project file must leave the mirror")
	}
}

func TestProjectPromptCarriesInstructionsAndFiles(t *testing.T) {
	p := &runProject{Project: &db.Project{Name: "竞品研究", Instructions: "结论用表格对比"}, files: []projectFile{{Rel: "list.csv", Size: 2048}}}
	got := p.prompt()
	for _, want := range []string{"[项目：竞品研究]", "结论用表格对比", "project/list.csv（2.0 KB）", "只有写到 project/"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

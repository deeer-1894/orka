package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	files, base, err := mirrorProjectIn(project, session)
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
	changed, _, err := publishProjectOut(session, project, base, time.Now())
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
	_, base, _ = mirrorProjectIn(project, session)
	if again, _, _ := publishProjectOut(session, project, base, time.Now()); len(again) != 0 {
		t.Fatalf("an unchanged mirror publishes nothing, got %v", again)
	}

	// A file deleted from the project leaves the mirror on the next run, so it
	// cannot be published back.
	if err := os.Remove(filepath.Join(project, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mirrorProjectIn(project, session); err != nil {
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

// Two runs (or a run and the user) work on one project at once. A run must
// publish only what IT changed: comparing its mirror with the project's
// current state would put back the other side's edits and deletions.
func TestProjectPublishLeavesOthersEditsAndReportsRealConflicts(t *testing.T) {
	project := t.TempDir()
	writeFile(t, filepath.Join(project, "a.md"), "a1")
	writeFile(t, filepath.Join(project, "b.md"), "b1")
	writeFile(t, filepath.Join(project, "gone.md"), "g1")
	runA, runB := t.TempDir(), t.TempDir()
	_, baseA, _ := mirrorProjectIn(project, runA)
	_, baseB, _ := mirrorProjectIn(project, runB)

	// Run A edits a.md and finishes first; meanwhile the user deletes gone.md.
	writeFile(t, filepath.Join(runA, "project", "a.md"), "a2")
	if changed, conflicts, err := publishProjectOut(runA, project, baseA, time.Now()); err != nil || len(changed) != 1 || len(conflicts) != 0 {
		t.Fatalf("run A: changed %v conflicts %v err %v", changed, conflicts, err)
	}
	if err := os.Remove(filepath.Join(project, "gone.md")); err != nil {
		t.Fatal(err)
	}

	// Run B touched only b.md, and also edited a.md from its stale copy.
	writeFile(t, filepath.Join(runB, "project", "b.md"), "b2")
	writeFile(t, filepath.Join(runB, "project", "a.md"), "a-from-b")
	now := time.Date(2026, 10, 26, 23, 1, 2, 0, time.UTC)
	changed, conflicts, err := publishProjectOut(runB, project, baseB, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") != "b.md" {
		t.Fatalf("run B published %v, want only b.md", changed)
	}
	if readFile(t, filepath.Join(project, "a.md")) != "a2" {
		t.Fatal("run A's edit must survive run B")
	}
	if len(conflicts) != 1 || conflicts[0] != "a.conflict-20261026-230102.md" || readFile(t, filepath.Join(project, conflicts[0])) != "a-from-b" {
		t.Fatalf("run B's competing edit is kept as a conflict copy, got %v", conflicts)
	}
	if _, err := os.Stat(filepath.Join(project, "gone.md")); !os.IsNotExist(err) {
		t.Fatal("a file deleted during the run must not come back")
	}
}

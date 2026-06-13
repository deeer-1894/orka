package middlewares

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplySkill(t *testing.T) {
	out, ok := applySkill("researcher")
	if !ok {
		t.Fatal("researcher should exist")
	}
	if !strings.Contains(out, "research analyst") {
		t.Errorf("unexpected guidance: %q", out)
	}

	// case-insensitive + trimmed
	if _, ok := applySkill("  Coder "); !ok {
		t.Error("skill lookup should be case-insensitive and trimmed")
	}

	msg, ok := applySkill("nope")
	if ok {
		t.Error("unknown skill should not be ok")
	}
	if !strings.Contains(msg, "Available:") {
		t.Errorf("error should list skills: %q", msg)
	}
}

func TestSkillsCatalog(t *testing.T) {
	cat := skillsCatalog()
	for _, n := range skillNames() {
		if !strings.Contains(cat, n) {
			t.Errorf("catalog missing %q", n)
		}
	}
	if !strings.Contains(cat, "apply_skill") {
		t.Error("catalog should mention the apply_skill tool")
	}
}

func TestParseSkillMD(t *testing.T) {
	def, err := parseSkillMD("---\nname: tester\ndescription: writes tests\n---\n\nBe rigorous.\nUse table tests.")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "tester" || def.Desc != "writes tests" {
		t.Fatalf("frontmatter = %+v", def)
	}
	if def.Prompt != "Be rigorous.\nUse table tests." {
		t.Fatalf("body = %q", def.Prompt)
	}
	if _, err := parseSkillMD("no frontmatter here"); err == nil {
		t.Error("expected error on missing frontmatter")
	}
}

func TestLoadAndCreateSkills(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "researcher2")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("---\nname: researcher2\ndescription: deep research v2\n---\n\nCross-check everything."), 0o644)

	n, err := LoadSkills(dir)
	if err != nil || n != 1 {
		t.Fatalf("LoadSkills n=%d err=%v", n, err)
	}
	if s, ok := skillByName("researcher2"); !ok || s.Prompt != "Cross-check everything." {
		t.Fatalf("loaded skill = %+v ok=%v", s, ok)
	}
	if _, ok := skillByName("writer"); !ok {
		t.Error("builtin skill lost after LoadSkills")
	}

	// skill_create persists + registers (case-insensitive)
	if err := RegisterSkill(SkillDef{Name: "MyTool", Desc: "d", Prompt: "do x"}, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := skillByName("mytool"); !ok {
		t.Error("created skill not registered")
	}
	if _, err := os.Stat(filepath.Join(dir, "mytool", "SKILL.md")); err != nil {
		t.Errorf("created skill not persisted: %v", err)
	}
	if err := RegisterSkill(SkillDef{Name: "bad name!", Desc: "d", Prompt: "p"}, false); err == nil {
		t.Error("expected invalid-name rejection")
	}
}

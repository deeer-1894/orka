package middlewares

import (
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

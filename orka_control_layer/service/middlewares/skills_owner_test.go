package middlewares

import (
	"context"
	"testing"
)

func TestPersonalSkillsArePrivateAndSystemReadonly(t *testing.T) {
	root := t.TempDir()
	a := WithSkillOwner(context.Background(), root, "a@test")
	b := WithSkillOwner(context.Background(), root, "b@test")
	def := SkillDef{Name: "private-review", Desc: "personal", Prompt: "private instructions"}
	if err := RegisterPersonalSkill(a, def); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := GetVisibleSkill(a, def.Name); err != nil || !ok {
		t.Fatal("owner cannot read", err)
	}
	if _, ok, err := GetVisibleSkill(b, def.Name); err != nil || ok {
		t.Fatal("cross owner read", err)
	}
	if err := DeletePersonalSkill(b, def.Name); err == nil {
		t.Fatal("cross owner delete")
	}
	if _, ok, _ := GetVisibleSkill(a, def.Name); !ok {
		t.Fatal("owner data removed")
	}
	if err := RegisterPersonalSkill(a, SkillDef{Name: "researcher", Prompt: "override"}); err == nil {
		t.Fatal("system overwrite")
	}
	if err := DeletePersonalSkill(a, "researcher"); err == nil {
		t.Fatal("system deletion")
	}
	if err := RegisterPersonalSkill(context.Background(), def); err == nil {
		t.Fatal("anonymous write")
	}
}

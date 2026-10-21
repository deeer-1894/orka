package middlewares

import (
	"context"
	"testing"
)

func TestDisabledSkillsDisappearForTheirOwnerOnly(t *testing.T) {
	base := t.TempDir()
	alice := WithSkillOwner(context.Background(), base, "alice@example.com")
	bob := WithSkillOwner(context.Background(), base, "bob@example.com")
	all := AllSkills()
	if len(all) == 0 {
		t.Skip("no builtin skills to disable")
	}
	name := all[0].Name
	if _, err := InstallPersonalSkill(alice, "---\nname: weekly-report\ndescription: Weekly reports\n---\nWrite the weekly report."); err != nil {
		t.Fatal(err)
	}
	if err := SetSkillEnabled(alice, name, false); err != nil {
		t.Fatal(err)
	}
	if err := SetSkillEnabled(alice, "weekly-report", false); err != nil {
		t.Fatal(err)
	}
	if err := SetSkillEnabled(alice, "no-such-skill", false); err == nil {
		t.Fatal("only a visible skill can be switched")
	}
	usable, err := UsableSkills(alice)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range usable {
		if d.Name == name || d.Name == "weekly-report" {
			t.Fatalf("%s is disabled but still usable", d.Name)
		}
	}
	if _, ok := SkillPromptFor(alice, name); ok {
		t.Fatal("a disabled skill must not reach the prompt")
	}
	if visible, _ := VisibleSkills(alice); len(visible) != len(all)+1 {
		t.Fatalf("disabled skills stay listed for management: %d", len(visible))
	}
	if _, ok := SkillPromptFor(bob, name); !ok {
		t.Fatal("another user's switch must not affect bob")
	}
	if err := SetSkillEnabled(alice, name, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := SkillPromptFor(alice, name); !ok {
		t.Fatal("re-enabled skill is usable again")
	}
}

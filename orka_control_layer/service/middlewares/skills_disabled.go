package middlewares

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// A user can switch a skill off without deleting it (builtin skills cannot be
// deleted at all). A disabled skill is gone from the composer menu, from
// find_skills and apply_skill, and from a project's default: runs simply do
// not see it. The set lives beside the owner's personal skills.

const disabledSkillsFile = ".disabled" // not *.json: never listed as a skill

func DisabledSkills(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	dir, err := personalSkillDir(ctx)
	if err != nil {
		return out
	}
	data, err := os.ReadFile(filepath.Join(dir, disabledSkillsFile))
	if err != nil {
		return out
	}
	var names []string
	if json.Unmarshal(data, &names) == nil {
		for _, n := range names {
			out[n] = true
		}
	}
	return out
}

// SetSkillEnabled switches a visible skill on or off for this owner.
func SetSkillEnabled(ctx context.Context, name string, enabled bool) error {
	name, err := personalSkillName(name)
	if err != nil {
		return err
	}
	if _, found, err := GetVisibleSkill(ctx, name); err != nil || !found {
		if err == nil {
			err = os.ErrNotExist
		}
		return err
	}
	dir, err := personalSkillDir(ctx)
	if err != nil {
		return err
	}
	set := DisabledSkills(ctx)
	if enabled {
		delete(set, name)
	} else {
		set[name] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	body, _ := json.Marshal(names)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".disabled-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, disabledSkillsFile))
}

// UsableSkills are the visible skills this owner has not switched off.
func UsableSkills(ctx context.Context) ([]SkillDef, error) {
	defs, err := VisibleSkills(ctx)
	if err != nil {
		return nil, err
	}
	off := DisabledSkills(ctx)
	out := defs[:0]
	for _, d := range defs {
		if !off[d.Name] {
			out = append(out, d)
		}
	}
	return out, nil
}

// GetUsableSkill is GetVisibleSkill for a skill that is also switched on.
func GetUsableSkill(ctx context.Context, name string) (SkillDef, bool, error) {
	def, ok, err := GetVisibleSkill(ctx, name)
	if !ok || err != nil || DisabledSkills(ctx)[def.Name] {
		return SkillDef{}, false, err
	}
	return def, true, nil
}

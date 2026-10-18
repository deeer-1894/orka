package middlewares

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type skillOwner struct{ base, owner string }
type skillOwnerKey struct{}

func WithSkillOwner(ctx context.Context, base, owner string) context.Context {
	return context.WithValue(ctx, skillOwnerKey{}, skillOwner{base, owner})
}
func personalSkillDir(ctx context.Context) (string, error) {
	scope, _ := ctx.Value(skillOwnerKey{}).(skillOwner)
	if scope.base == "" || strings.TrimSpace(scope.owner) == "" {
		return "", errors.New("personal skills require an authenticated owner and persistent storage")
	}
	digest := sha256.Sum256([]byte(scope.owner))
	return filepath.Join(scope.base, ".orka_skills", hex.EncodeToString(digest[:])), nil
}
func personalSkillName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !validSkillName(name) || len(name) > 100 {
		return "", errors.New("invalid skill name")
	}
	return name, nil
}

// VisibleSkills merges immutable system skills with only this owner's files.
// Personal state is not cached in a process-global registry.
func VisibleSkills(ctx context.Context) ([]SkillDef, error) {
	out := AllSkills()
	dir, err := personalSkillDir(ctx)
	if err != nil {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if _, exists := GetSkill(name); exists {
			continue
		}
		def, ok, err := GetVisibleSkill(ctx, name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func GetVisibleSkill(ctx context.Context, name string) (SkillDef, bool, error) {
	name, err := personalSkillName(name)
	if err != nil {
		return SkillDef{}, false, err
	}
	if def, ok := GetSkill(name); ok {
		return def, true, nil
	}
	dir, err := personalSkillDir(ctx)
	if err != nil {
		return SkillDef{}, false, nil
	}
	root, err := os.OpenRoot(dir)
	if os.IsNotExist(err) {
		return SkillDef{}, false, nil
	}
	if err != nil {
		return SkillDef{}, false, err
	}
	defer root.Close()
	file, err := root.Open(name + ".json")
	if os.IsNotExist(err) {
		return SkillDef{}, false, nil
	}
	if err != nil {
		return SkillDef{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return SkillDef{}, false, err
	}
	if info.Size() > 256<<10 {
		return SkillDef{}, false, errors.New("skill exceeds 256 KiB")
	}
	var def SkillDef
	err = json.NewDecoder(file).Decode(&def)
	if err != nil {
		return SkillDef{}, false, err
	}
	if def.Name != name {
		return SkillDef{}, false, errors.New("skill identity mismatch")
	}
	return def, true, nil
}
func RegisterPersonalSkill(ctx context.Context, def SkillDef) error {
	name, err := personalSkillName(def.Name)
	if err != nil {
		return err
	}
	def.Name = name
	if _, system := GetSkill(name); system {
		return errors.New("system skills are read-only; choose a personal name")
	}
	if strings.TrimSpace(def.Prompt) == "" || strings.TrimSpace(def.Desc) == "" {
		return errors.New("description and instructions are required")
	}
	body, err := json.Marshal(def)
	if err != nil {
		return err
	}
	if len(body) > 256<<10 {
		return errors.New("skill exceeds 256 KiB")
	}
	dir, err := personalSkillDir(ctx)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".skill-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name+".json"))
}
func DeletePersonalSkill(ctx context.Context, name string) error {
	name, err := personalSkillName(name)
	if err != nil {
		return err
	}
	if _, system := GetSkill(name); system {
		return errors.New("system skills are read-only")
	}
	dir, err := personalSkillDir(ctx)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(name + ".json")
}
func InstallPersonalSkill(ctx context.Context, content string) (string, error) {
	def, err := parseSkillMD(content)
	if err != nil {
		return "", err
	}
	if err = RegisterPersonalSkill(ctx, def); err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(def.Name)), nil
}
func SkillPromptFor(ctx context.Context, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	def, ok, err := GetVisibleSkill(ctx, name)
	return def.Prompt, ok && err == nil
}

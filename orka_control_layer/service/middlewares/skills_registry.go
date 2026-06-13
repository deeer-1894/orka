package middlewares

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ApplySkillTool is the built-in tool the model calls to adopt a skill. A skill
// is an expertise "prompt pack" (Claude-Code-style: a SKILL.md with name +
// description frontmatter and a markdown body of instructions). The catalog only
// exposes name+description; the body is loaded on demand when adopted, so it
// scales to large skills without bloating every prompt (progressive disclosure).
const ApplySkillTool = "apply_skill"

// SkillDef is a named expertise the agent can adopt mid-conversation.
type SkillDef struct {
	Name   string
	Desc   string // one-liner shown in the catalog so the model knows when to use it
	Prompt string // the expert guidance (SKILL.md body) injected when adopted
}

// skills is the live registry: builtins seeded below, plus any SKILL.md packages
// loaded from the global skills dir (LoadSkills) and any created at runtime
// (skill_create). Guarded because skill_create mutates it concurrently.
var (
	skillMu    sync.RWMutex
	skills     = map[string]SkillDef{}
	skillsRoot string // global skills dir; "" until LoadSkills is called
)

func init() {
	for k, v := range builtinSkills {
		skills[k] = v
	}
}

// builtinSkills is the default catalog seeded into the registry at init.
var builtinSkills = map[string]SkillDef{
	"researcher": {
		Name: "researcher",
		Desc: "Deep web research with cross-checked, cited findings.",
		Prompt: "You are now operating as a rigorous research analyst. Method:\n" +
			"1. Decompose the question into sub-questions.\n" +
			"2. Use `web_search` for each, then `fetch_url` to read the most authoritative results (prefer primary sources/official docs).\n" +
			"3. Cross-check facts across at least two independent sources; flag disagreements.\n" +
			"4. Answer with a short conclusion first, then key findings as bullets, each followed by its source URL in parentheses.\n" +
			"5. Never fabricate sources or numbers. If evidence is thin, say so explicitly.",
	},
	"writer": {
		Name: "writer",
		Desc: "Polished long-form / professional writing (articles, emails, docs).",
		Prompt: "You are now operating as a professional writer/editor. Guidelines:\n" +
			"- Open with the main point; structure with clear headings and short paragraphs.\n" +
			"- Match tone to the audience (formal for business, warm for personal).\n" +
			"- Prefer concrete verbs and specifics over filler. Cut redundancy.\n" +
			"- Use Markdown formatting. End with a brief, actionable closing where appropriate.",
	},
	"coder": {
		Name: "coder",
		Desc: "Senior software engineering: design, code, debugging, reviews.",
		Prompt: "You are now operating as a senior software engineer. Guidelines:\n" +
			"- Give correct, runnable code with the language fenced in Markdown.\n" +
			"- State key design decisions and trade-offs briefly; call out edge cases and error handling.\n" +
			"- Prefer standard-library / idiomatic solutions; avoid unnecessary dependencies.\n" +
			"- When debugging, reason about the root cause before proposing a fix.",
	},
	"analyst": {
		Name: "analyst",
		Desc: "Structured problem-solving, comparisons, data reasoning, planning.",
		Prompt: "You are now operating as an analytical consultant. Method:\n" +
			"- Restate the goal and the decision to be made.\n" +
			"- Break the problem into clear dimensions; compare options in a Markdown table when useful.\n" +
			"- Use `calculator` for any arithmetic and `current_time` for date math — do not guess numbers.\n" +
			"- End with a concrete recommendation and the main risks/assumptions.",
	},
	"translator": {
		Name: "translator",
		Desc: "Accurate, natural translation that preserves tone and intent.",
		Prompt: "You are now operating as a professional translator. Guidelines:\n" +
			"- Produce natural, idiomatic text in the target language — not word-for-word.\n" +
			"- Preserve register, tone, and any domain terminology; keep proper nouns intact.\n" +
			"- If the target language is ambiguous, default to the user's own language.\n" +
			"- Output only the translation unless asked to explain choices.",
	},
}

// skillByName returns a skill (case-insensitive), and whether it exists.
func skillByName(name string) (SkillDef, bool) {
	skillMu.RLock()
	defer skillMu.RUnlock()
	s, ok := skills[strings.ToLower(strings.TrimSpace(name))]
	return s, ok
}

// skillNames returns the catalog keys, sorted for stable prompts/tests.
func skillNames() []string {
	skillMu.RLock()
	defer skillMu.RUnlock()
	names := make([]string, 0, len(skills))
	for k := range skills {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// AllSkills returns every registered skill (sorted), for the find_skills tool.
func AllSkills() []SkillDef {
	out := make([]SkillDef, 0)
	for _, n := range skillNames() {
		if s, ok := skillByName(n); ok {
			out = append(out, s)
		}
	}
	return out
}

// skillsCatalog renders the one-liner catalog appended to the system prompt so
// the model knows which skills exist and when to adopt one.
func skillsCatalog() string {
	var sb strings.Builder
	sb.WriteString("\nAvailable skills (call `apply_skill` with one of these names to adopt that expertise before answering, when it would improve the result; use `find_skills` to search and `skill_create` to author a new one):\n")
	for _, n := range skillNames() {
		s, _ := skillByName(n)
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Desc)
	}
	return sb.String()
}

// LoadSkills scans <dir>/<name>/SKILL.md packages and registers them, merging
// over the builtins. Returns the number loaded. A missing dir is not an error.
func LoadSkills(dir string) (int, error) {
	skillMu.Lock()
	skillsRoot = dir
	skillMu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(dir, e.Name(), "SKILL.md"))
		if rerr != nil {
			continue
		}
		def, perr := parseSkillMD(string(b))
		if perr != nil || def.Name == "" {
			continue
		}
		skillMu.Lock()
		skills[strings.ToLower(def.Name)] = def
		skillMu.Unlock()
		n++
	}
	return n, nil
}

// parseSkillMD parses a Claude-Code-style SKILL.md: a YAML-ish frontmatter block
// (name, description) delimited by --- lines, then a markdown body (the prompt).
func parseSkillMD(content string) (SkillDef, error) {
	s := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(strings.TrimSpace(s), "---") {
		return SkillDef{}, fmt.Errorf("missing frontmatter")
	}
	s = strings.TrimSpace(s)
	rest := strings.TrimPrefix(s, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return SkillDef{}, fmt.Errorf("unterminated frontmatter")
	}
	front := rest[:end]
	body := strings.TrimSpace(rest[end+len("\n---"):])
	var def SkillDef
	for _, ln := range strings.Split(front, "\n") {
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		val := strings.Trim(strings.TrimSpace(v), "\"'")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "name":
			def.Name = val
		case "description", "desc":
			def.Desc = val
		}
	}
	def.Prompt = body
	return def, nil
}

// RegisterSkill adds a skill to the live registry and, when persist is set and a
// skills dir is configured, writes it as <dir>/<name>/SKILL.md so it survives a
// restart. Returns an error on an invalid name or write failure.
func RegisterSkill(def SkillDef, persist bool) error {
	name := strings.ToLower(strings.TrimSpace(def.Name))
	if name == "" || !validSkillName(name) {
		return fmt.Errorf("invalid skill name %q (use letters/digits/_/-)", def.Name)
	}
	def.Name = name
	skillMu.Lock()
	skills[name] = def
	dir := skillsRoot
	skillMu.Unlock()
	if persist && dir != "" {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		md := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, def.Desc, def.Prompt)
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func validSkillName(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return s != ""
}

// SkillPrompt returns a skill's expert guidance for deterministic injection into
// the system prompt when the user has explicitly locked a skill "mode" in the UI
// (vs. the model adopting one itself via apply_skill).
func SkillPrompt(name string) (string, bool) {
	s, ok := skillByName(name)
	if !ok {
		return "", false
	}
	return s.Prompt, true
}

// applySkill resolves the requested skill and returns the tool-result content
// (the expert guidance) plus whether it was found.
func applySkill(name string) (string, bool) {
	s, ok := skillByName(name)
	if !ok {
		return fmt.Sprintf("Unknown skill %q. Available: %s", name, strings.Join(skillNames(), ", ")), false
	}
	return "Adopted skill \"" + s.Name + "\". Follow this guidance for the rest of this task:\n\n" + s.Prompt, true
}

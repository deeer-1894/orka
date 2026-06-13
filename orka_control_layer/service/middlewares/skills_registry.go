package middlewares

import (
	"fmt"
	"sort"
	"strings"
)

// ApplySkillTool is the built-in tool the model calls to adopt a skill. A skill
// is an expertise "prompt pack": its guidance is returned as the tool result,
// so the model reads it and operates accordingly for the rest of the turn. This
// keeps skills model-driven (the agent picks the right expertise itself) without
// breaking the tool/assistant message ordering that strict providers require.
const ApplySkillTool = "apply_skill"

// SkillDef is a named expertise the agent can adopt mid-conversation.
type SkillDef struct {
	Name   string
	Desc   string // one-liner shown in the catalog so the model knows when to use it
	Prompt string // the expert guidance injected when adopted
}

// builtinSkills is the default catalog. New skills are added here; nothing else
// needs to change (catalog + tool enum are derived from this map).
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
	s, ok := builtinSkills[strings.ToLower(strings.TrimSpace(name))]
	return s, ok
}

// skillNames returns the catalog keys, sorted for stable prompts/tests.
func skillNames() []string {
	names := make([]string, 0, len(builtinSkills))
	for k := range builtinSkills {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// skillsCatalog renders the one-liner catalog appended to the system prompt so
// the model knows which skills exist and when to adopt one.
func skillsCatalog() string {
	var sb strings.Builder
	sb.WriteString("\nAvailable skills (call `apply_skill` with one of these names to adopt that expertise before answering, when it would improve the result):\n")
	for _, n := range skillNames() {
		s := builtinSkills[n]
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Desc)
	}
	return sb.String()
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

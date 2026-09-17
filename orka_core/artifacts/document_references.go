package artifacts

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

var documentReferenceTokens = regexp.MustCompile("`([^`\\n]+)`|\\[[^\\]\\n]*\\]\\(([^)\\n]+)\\)")

// Extract conservative file candidates, not a complete Markdown dependency
// graph. Fenced/indented examples and remote URLs are ignored. Advisory results
// may still describe generated outputs; callers must not treat absence as proof
// of a broken deliverable. Keeping parsing separate makes its coverage explicit.
func documentFileReferences(text string) []string {
	var refs []string
	seen := map[string]bool{}
	var fence byte
	fenceLength := 0
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && (trimmed[0] == '`' || trimmed[0] == '~') {
			n := 0
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence, fenceLength = trimmed[0], n
				} else if fence == trimmed[0] && n >= fenceLength && strings.TrimSpace(trimmed[n:]) == "" {
					fence = 0
				}
				continue
			}
		}
		if fence != 0 || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		for _, token := range documentReferenceTokens.FindAllStringSubmatch(line, -1) {
			ref := token[1]
			// A bare code filename may be a command example or a basename in a
			// different documented directory. Only explicit paths are actionable.
			if token[2] == "" && !strings.Contains(ref, "/") {
				continue
			}
			if token[2] != "" {
				raw := strings.TrimSpace(token[2])
				if strings.HasPrefix(raw, "<") {
					if end := strings.Index(raw, ">"); end > 0 {
						raw = raw[1:end]
					} else {
						continue
					}
				} else if i := strings.IndexAny(raw, " \t"); i >= 0 {
					raw = raw[:i]
				}
				u, err := url.Parse(raw)
				if err != nil || u.IsAbs() || u.Host != "" {
					continue
				}
				ref = u.Path
			}
			if ref == "" || strings.HasPrefix(ref, "/") || strings.ContainsAny(ref, "\\\x00\r\n\t :*$<>|()={}") || strings.HasSuffix(ref, "/") {
				continue
			}
			switch strings.ToLower(path.Ext(ref)) {
			case ".log", ".txt", ".json", ".csv", ".tsv", ".md", ".markdown", ".html", ".png", ".svg", ".pdf", ".zip":
			default:
				continue
			}
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

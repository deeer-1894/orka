package tools

import (
	"net/url"
	"path"
	"sort"
	"strings"
)

// Recognize index URL conventions, not arbitrary Markdown document links.
func isLLMSCatalog(u *url.URL) bool {
	p := strings.ToLower(u.Path)
	return path.Base(p) == "llms.txt" || (strings.Contains(p, "/_llms/") && (strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".txt")))
}

// A small set of common locale codes avoids mistaking arbitrary short path
// segments for languages. Regional variants use their base language.
func docLanguage(p string) string {
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		part = strings.TrimSuffix(strings.TrimSuffix(part, ".md"), ".txt")
		part, _, _ = strings.Cut(part, "-")
		if len(part) == 2 && strings.Contains(" ar de en es fr hi id it ja ko nl pl pt ru tr uk vi zh ", " "+part+" ") {
			return part
		}
	}
	return ""
}

func docLanguageRank(raw, preferred string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 3
	}
	switch language := docLanguage(u.Path); language {
	case preferred:
		return 0
	case "en":
		return 1
	case "":
		return 2
	default:
		return 3
	}
}

// Prefer the supplied locale, English, then unlocalized indexes. Within a
// language choose shallower catalogs before version branches, then URL order.
// Do not spend the remaining requests expanding translations of the same docs.
func preferredDocIndexes(indexes []*url.URL, language string) []*url.URL {
	sort.Slice(indexes, func(i, j int) bool {
		a, b := docLanguageRank(indexes[i].String(), language), docLanguageRank(indexes[j].String(), language)
		if a != b {
			return a < b
		}
		a, b = strings.Count(indexes[i].Path, "/"), strings.Count(indexes[j].Path, "/")
		if a != b {
			return a < b
		}
		return indexes[i].String() < indexes[j].String()
	})
	if len(indexes) == 0 {
		return indexes
	}
	selected := docLanguage(indexes[0].Path)
	kept := indexes[:0]
	for _, u := range indexes {
		if docLanguage(u.Path) == selected {
			kept = append(kept, u)
		}
	}
	return kept
}

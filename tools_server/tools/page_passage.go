package tools

import (
	"strings"
	"unicode/utf8"
)

// Retain a short heading outside the selected passage (but inside the excerpt
// budget). Query terms already supplied by that heading cannot make a generic
// introduction outrank the body passage containing the remaining specific terms.
func sectionExcerpt(text string, terms []string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	prefix := ""
	if heading, body, ok := strings.Cut(text, "\n"); ok && reMarkdownHeading.MatchString(heading) {
		candidate := heading + "\n\n"
		if size := utf8.RuneCountInString(candidate); size <= max/3 {
			prefix = candidate
			text = strings.TrimSpace(body)
			max -= size
		}
	}
	var bodyTerms []string
	for _, term := range terms {
		if !strings.Contains(strings.ToLower(prefix), term) {
			bodyTerms = append(bodyTerms, term)
		}
	}
	if len(bodyTerms) == 0 {
		return prefix + truncatePageChars(text, max)
	}
	return prefix + bestPagePassage(text, bodyTerms, max)
}

// Scan overlapping, fixed-size windows across the entire bounded page. Coverage
// counts distinct query terms, not frequency; ties prefer a match near the first
// quarter of a window, leaving useful context after it, then document order.
// Half-window steps keep work proportional to page size rather than the number
// of repeated matches. The two reserved characters hold omission markers.
func bestPagePassage(text string, terms []string, max int) string {
	if utf8.RuneCountInString(text) <= max || max < 4 {
		return truncatePageChars(text, max)
	}
	runes := []rune(text)
	lower := []rune(strings.ToLower(text))
	width := max - 2
	step := width / 2
	bestStart, bestScore, bestDistance := 0, -1, width
	for start := 0; start < len(runes); start += step {
		if start+width > len(runes) {
			start = len(runes) - width
		}
		window := string(lower[start : start+width])
		score, first := 0, width
		for _, term := range terms {
			if pos := strings.Index(window, term); pos >= 0 {
				score++
				if n := utf8.RuneCountInString(window[:pos]); n < first {
					first = n
				}
			}
		}
		distance := first - width/4
		if distance < 0 {
			distance = -distance
		}
		if score > bestScore || (score == bestScore && distance < bestDistance) {
			bestStart, bestScore, bestDistance = start, score, distance
		}
		if start+width == len(runes) {
			break
		}
	}
	end := bestStart + width
	out := string(runes[bestStart:end])
	if bestStart > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}

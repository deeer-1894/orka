package tools

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

var (
	reHead            = regexp.MustCompile(`(?is)<head\b[^>]*>.*?</head\s*>`)
	reMain            = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main\s*>`)
	reArticle         = regexp.MustCompile(`(?is)<article\b[^>]*>(.*?)</article\s*>`)
	reHeading         = regexp.MustCompile(`(?is)<h[1-6]\b[^>]*>(.*?)</h[1-6]\s*>`)
	reBlock           = regexp.MustCompile(`(?i)</?(?:p|div|section|li|ul|ol|pre|blockquote|tr|br|hr)\b[^>]*>`)
	reHTMLPage        = regexp.MustCompile(`(?i)<(?:html|head|body|main|article|h[1-6]|p|div|pre)\b`)
	reMarkdownHeading = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*\s*$`)
)

type readablePage struct{ Title, Text string }

// Keep line and heading boundaries for targeted reads. This intentionally uses
// the existing lightweight HTML cleanup rather than introducing a browser.
func extractPage(p loadedPage) readablePage {
	body, title := p.Body, ""
	isHTML := pageIsHTML(p)
	if isHTML {
		if m := reTitle.FindStringSubmatch(body); len(m) > 1 {
			title = clean(m[1])
		}
		body = reScript.ReplaceAllString(body, " ")
		body = reStyle.ReplaceAllString(body, " ")
		body = reHead.ReplaceAllString(body, " ")
		if m := reMain.FindStringSubmatch(body); len(m) > 1 {
			body = m[1]
		} else if articles := reArticle.FindAllStringSubmatch(body, -1); len(articles) > 0 {
			var content strings.Builder
			for _, article := range articles {
				content.WriteString(article[1])
				content.WriteByte('\n')
			}
			body = content.String()
		}
		body = reChrome.ReplaceAllString(body, " ")
		body = reHeading.ReplaceAllStringFunc(body, func(h string) string { return "\n\n## " + clean(reHeading.FindStringSubmatch(h)[1]) + "\n\n" })
		body = reBlock.ReplaceAllString(body, "\n")
		body = reTags.ReplaceAllString(body, "")
		body = html.UnescapeString(body)
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, line)
	}
	text := strings.TrimSpace(strings.Join(out, "\n"))
	if !isHTML {
		title = markdownPageTitle(text)
	}
	return readablePage{strings.Join(strings.Fields(title), " "), text}
}

func queryTerms(query string) []string {
	seen := map[string]bool{}
	var terms []string
	split := func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '-' && r != '_')
	}
	for _, term := range strings.FieldsFunc(strings.ToLower(query), split) {
		if !seen[term] {
			terms = append(terms, term)
			seen[term] = true
		}
	}
	return terms
}

// Count each term once, so repetition cannot swamp a focused title match.
func textScore(text string, terms []string) int {
	text = strings.ToLower(text)
	score := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			score++
		}
	}
	return score
}

// Explicit Markdown types take precedence. When servers label a document as
// plain text, only leading HTML markup is evidence of an HTML page; embedded
// snippets in Markdown prose or fences must not change the document format.
func pageIsHTML(p loadedPage) bool {
	mediaType, _, _ := strings.Cut(strings.ToLower(p.ContentType), ";")
	switch strings.TrimSpace(mediaType) {
	case "text/markdown", "text/x-markdown":
		return false
	case "text/html", "application/xhtml+xml":
		return true
	}
	if p.URL != nil && strings.HasSuffix(strings.ToLower(p.URL.Path), ".md") {
		return false
	}
	body := strings.TrimSpace(strings.TrimPrefix(p.Body, "\uFEFF"))
	match := reHTMLPage.FindStringIndex(body)
	return match != nil && match[0] == 0
}

func markdownPageTitle(text string) string {
	var fence string
	for _, line := range strings.Split(text, "\n") {
		if markdownCodeLine(line, &fence) {
			continue
		}
		if heading := reMarkdownHeading.FindStringSubmatch(strings.TrimSpace(line)); len(heading) > 1 {
			return heading[1]
		}
	}
	return ""
}

// Include fence delimiters themselves, and require a closing fence at least as
// long as its opener. Headings in examples are content, not page structure.
func markdownCodeLine(line string, fence *string) bool {
	line = strings.TrimSpace(line)
	if *fence != "" {
		if len(line) >= len(*fence) && strings.Trim(line, (*fence)[:1]) == "" {
			*fence = ""
		}
		return true
	}
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return false
	}
	end := 0
	for end < len(line) && line[end] == line[0] {
		end++
	}
	if end < 3 {
		return false
	}
	*fence = line[:end]
	return true
}

// Source metadata has its own lines and is never spent from the excerpt budget.
// An empty Title header honestly represents a page without a discoverable title.
func formatPageText(url, title, body string) string {
	return "URL: " + url + "\nTitle: " + title + "\n\n" + body
}

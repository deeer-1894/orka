package tools

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func readSection() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		u, err := parsePageURL(req.GetString("url", ""), false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		query := strings.TrimSpace(req.GetString("query", ""))
		if query == "" || len(query) > 512 {
			return mcp.NewToolResultError("query is required and must be at most 512 bytes"), nil
		}
		max, err := pageIntArgument(req, "max_chars", 4000, 20000)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		page, err := loadPage(ctx, u, nil, maxPageBytes)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		readable := extractPage(page)
		text := selectSections(readable.Text, query, max)
		if text == "" {
			return mcp.NewToolResultError("no readable section matched the query; try another term or use fetch_url"), nil
		}
		return mcp.NewToolResultText(formatPageText(page.URL.String(), readable.Title, text)), nil
	}
}

func pageIntArgument(req mcp.CallToolRequest, name string, fallback, max int) (int, error) {
	if _, present := req.GetArguments()[name]; !present {
		return fallback, nil
	}
	n := req.GetFloat(name, math.NaN())
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 1 || n > float64(max) || n != math.Trunc(n) {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d", name, max)
	}
	return int(n), nil
}

type pageSection struct {
	title, text  string
	score, order int
}

func selectSections(text, query string, max int) string {
	terms := queryTerms(query)
	var sections []pageSection
	current := pageSection{}
	flush := func() {
		current.text = strings.TrimSpace(current.text)
		current.score = 3*textScore(current.title, terms) + textScore(current.text, terms)
		current.order = len(sections)
		if current.text != "" {
			sections = append(sections, current)
		}
	}
	// A builder avoids quadratic concatenation on pages with many lines.
	var body strings.Builder
	var fence string
	for _, line := range strings.Split(text, "\n") {
		if m := reMarkdownHeading.FindStringSubmatch(strings.TrimSpace(line)); !markdownCodeLine(line, &fence) && len(m) > 1 {
			current.text = body.String()
			flush()
			body.Reset()
			current = pageSection{title: m[1]}
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	current.text = body.String()
	flush()
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].score > sections[j].score })
	var chosen []pageSection
	remaining := max
	for _, section := range sections {
		if section.score == 0 || remaining <= 0 {
			break
		}
		section.text = sectionExcerpt(section.text, terms, remaining)
		chosen = append(chosen, section)
		remaining -= utf8.RuneCountInString(section.text) + 2
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].order < chosen[j].order })
	parts := make([]string, len(chosen))
	for i, section := range chosen {
		parts[i] = section.text
	}
	return truncatePageChars(strings.Join(parts, "\n\n"), max)
}

package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func discoverDocs() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		u, err := parsePageURL(req.GetString("url", ""), false)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		query := strings.TrimSpace(req.GetString("query", ""))
		if len(query) > 512 {
			return mcp.NewToolResultError("query must be at most 512 bytes"), nil
		}
		limit, err := pageIntArgument(req, "limit", 10, 20)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		discovery := docsDiscovery{origin: u, language: docLanguage(u.Path), remaining: maxIndexLoads, visited: map[string]bool{}}
		if discovery.language == "" {
			discovery.language = "en"
		}
		var failures []string
		for _, candidate := range indexCandidates(u) {
			if ctx.Err() != nil {
				failures = append(failures, ctx.Err().Error())
				break
			}
			if discovery.remaining == 0 {
				failures = append(failures, "discovery request budget exhausted (maximum 5)")
				break
			}
			links, source, err := discovery.readIndex(ctx, candidate, candidate.String() == u.String())
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			out, err := json.Marshal(struct {
				IndexSource string    `json:"index_source"`
				Results     []docLink `json:"results"`
			}{source, rankDocLinks(links, query, limit, discovery.language)})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(out)), nil
		}
		return mcp.NewToolResultError("no usable documentation index; supply an official Markdown, HTML or sitemap index URL. " + strings.Join(failures, "; ")), nil
	}
}

func indexCandidates(u *url.URL) []*url.URL {
	// Explicit index files are read directly. A site/page URL probes standard
	// indexes in the supplied directory first, then its landing page, then root.
	lower := strings.ToLower(u.Path)
	for _, suffix := range []string{".txt", ".md", ".xml", ".html"} {
		if strings.HasSuffix(lower, suffix) {
			return []*url.URL{u}
		}
	}
	base := *u
	base.RawPath = ""
	base.RawQuery = ""
	base.ForceQuery = false
	directory := strings.TrimRight(u.Path, "/")
	var candidates []*url.URL
	add := func(path string) { next := base; next.Path = path; candidates = append(candidates, &next) }
	if directory != "" {
		add(directory + "/llms.txt")
		add(directory + "/sitemap.xml")
		candidates = append(candidates, u)
	}
	add("/llms.txt")
	add("/sitemap.xml")
	return candidates
}

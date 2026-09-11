package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// All traversal state and the HTTP request budget belong to one invocation.
// No cache or discovery state is shared across users or tool calls.
type docsDiscovery struct {
	origin    *url.URL
	language  string
	remaining int
	visited   map[string]bool
}

type pendingDocIndex struct {
	url     *url.URL
	depth   int
	landing bool
}

// Expand only known catalogs/sitemap children, or a supplied HTML landing page
// with one directory link. Every expansion uses the same single child level.
func (d *docsDiscovery) readIndex(ctx context.Context, start *url.URL, landing bool) ([]docLink, string, error) {
	queue := []pendingDocIndex{{start, 0, landing}}
	results := map[string]docLink{}
	var failures []string
	source := start.String()
	for len(queue) > 0 && d.remaining > 0 && ctx.Err() == nil {
		next := queue[0]
		queue = queue[1:]
		if d.visited[next.url.String()] {
			continue
		}
		d.visited[next.url.String()] = true
		page, err := loadPageWithinBudget(ctx, next.url, d.origin, maxIndexBytes, &d.remaining)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		d.visited[page.URL.String()] = true
		if next.depth == 0 {
			source = page.URL.String()
		}
		index, err := parseDocsIndex(page)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", page.URL, err))
			continue
		}
		// A successfully expanded catalog is replaced by its children.
		delete(results, next.url.String())
		links := d.resolvedLinks(page, index.links)
		singleLanding := false
		if next.landing && pageIsHTML(page) && len(links) == 1 {
			target, _ := url.Parse(links[0].URL) // Already validated by resolvedLinks.
			singleLanding = strings.HasSuffix(target.Path, "/")
		}
		var children []*url.URL
		for _, link := range links {
			if d.visited[link.URL] {
				continue
			}
			if singleLanding {
				link.Kind = "index"
			}
			if len(results) >= maxIndexLinks {
				break
			}
			if _, exists := results[link.URL]; !exists {
				results[link.URL] = link
			}
			if next.depth == 0 && link.Kind == "index" {
				child, _ := url.Parse(link.URL)
				children = append(children, child)
			}
		}
		if next.depth != 0 {
			continue
		}
		// Sitemap references remain traversal-only, preserving existing output.
		for _, raw := range index.children {
			child := resolveDocURL(page.URL, d.origin, raw)
			if child != nil && !d.visited[child.String()] {
				children = append(children, child)
			}
		}
		queued := map[string]bool{}
		for _, child := range preferredDocIndexes(children, d.language) {
			if queued[child.String()] {
				continue
			}
			queued[child.String()] = true
			if len(queue) >= d.remaining {
				break
			}
			queue = append(queue, pendingDocIndex{child, 1, singleLanding})
		}
	}
	links := make([]docLink, 0, len(results))
	for _, link := range results {
		links = append(links, link)
	}
	if len(links) > 0 {
		return links, source, nil
	}
	if len(failures) == 0 {
		failures = append(failures, "no same-origin page links within index traversal bounds")
	}
	return nil, source, fmt.Errorf("%s: %s", source, strings.Join(failures, "; "))
}

func (d *docsDiscovery) resolvedLinks(page loadedPage, links []docLink) []docLink {
	seen := map[string]bool{}
	var out []docLink
	for _, link := range links {
		u := resolveDocURL(page.URL, d.origin, link.URL)
		if u == nil || seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		kind := "page"
		if isLLMSCatalog(u) {
			kind = "index"
		}
		out = append(out, docLink{Title: docTitle(link, u), URL: u.String(), Kind: kind})
	}
	return out
}

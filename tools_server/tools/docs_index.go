package tools

import (
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	maxIndexBytes = 512 << 10
	maxIndexLinks = 5000
	maxIndexLoads = 5
)

type docLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}
type docsIndex struct {
	links    []docLink
	children []string
}

var (
	reDocMarkdownLink = regexp.MustCompile(`\[([^\]\n]+)\]\(<?([^\s)>]+)>?(?:\s+"[^"\n]*")?\)`)
	reDocAnchor       = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>(.*?)</a\s*>`)
)

func parseDocsIndex(page loadedPage) (docsIndex, error) {
	body := strings.TrimSpace(page.Body)
	var index docsIndex
	if strings.HasPrefix(body, "<?xml") || strings.HasPrefix(body, "<urlset") || strings.HasPrefix(body, "<sitemapindex") {
		var sitemap struct {
			XMLName xml.Name
			URLs    []struct {
				Loc string `xml:"loc"`
			} `xml:"url"`
			Maps []struct {
				Loc string `xml:"loc"`
			} `xml:"sitemap"`
		}
		if err := xml.Unmarshal([]byte(body), &sitemap); err != nil {
			return index, fmt.Errorf("invalid sitemap XML: %w", err)
		}
		switch sitemap.XMLName.Local {
		case "urlset":
			for _, item := range sitemap.URLs {
				index.links = append(index.links, docLink{URL: strings.TrimSpace(item.Loc)})
				if len(index.links) >= maxIndexLinks {
					break
				}
			}
		case "sitemapindex":
			for _, item := range sitemap.Maps {
				index.children = append(index.children, strings.TrimSpace(item.Loc))
				if len(index.children) >= maxIndexLinks {
					break
				}
			}
		default:
			return index, fmt.Errorf("expected sitemap urlset or sitemapindex")
		}
		return index, nil
	}
	isHTML := strings.Contains(page.ContentType, "text/html") || reHTMLPage.MatchString(body)
	if isHTML {
		if strings.HasSuffix(page.URL.Path, ".txt") || strings.HasSuffix(page.URL.Path, ".md") {
			return index, fmt.Errorf("expected a text index, received HTML")
		}
		body = reScript.ReplaceAllString(body, "")
		body = reStyle.ReplaceAllString(body, "")
		for _, m := range reDocAnchor.FindAllStringSubmatch(body, maxIndexLinks) {
			href := m[1]
			if href == "" {
				href = m[2]
			}
			index.links = append(index.links, docLink{Title: clean(m[3]), URL: html.UnescapeString(href)})
		}
	} else {
		for _, m := range reDocMarkdownLink.FindAllStringSubmatch(body, maxIndexLinks) {
			index.links = append(index.links, docLink{Title: clean(m[1]), URL: m[2]})
		}
	}
	if len(index.links) == 0 {
		return index, fmt.Errorf("index contains no page links")
	}
	return index, nil
}

func resolveDocURL(base, origin *url.URL, raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") || len(raw) > 2048 {
		return nil
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	resolved := base.ResolveReference(ref)
	u, err := parsePageURL(resolved.String(), false)
	if err != nil || !sameOrigin(u, origin) {
		return nil
	}
	// Normalize host spelling/default ports before deduplication.
	u.Host = origin.Host
	return u
}

func rankDocLinks(links []docLink, query string, limit int, language string) []docLink {
	terms := queryTerms(query)
	score := func(link docLink) int { return 3*textScore(link.Title, terms) + textScore(link.URL, terms) }
	sort.Slice(links, func(i, j int) bool {
		if links[i].Kind != links[j].Kind {
			return links[i].Kind == "page"
		}
		// A language preference breaks ties; it must not hide a specific match.
		a, b := score(links[i]), score(links[j])
		if a != b {
			return a > b
		}
		aLang, bLang := docLanguageRank(links[i].URL, language), docLanguageRank(links[j].URL, language)
		if aLang != bLang {
			return aLang < bLang
		}
		return links[i].URL < links[j].URL
	})
	if len(links) > limit {
		links = links[:limit]
	}
	return links
}

func docTitle(link docLink, u *url.URL) string {
	title := strings.Join(strings.Fields(link.Title), " ")
	if title == "" {
		title = path.Base(strings.TrimSuffix(u.Path, "/"))
		if decoded, err := url.PathUnescape(title); err == nil {
			title = decoded
		}
		title = strings.NewReplacer("-", " ", "_", " ").Replace(title)
	}
	return truncatePageChars(title, 200)
}

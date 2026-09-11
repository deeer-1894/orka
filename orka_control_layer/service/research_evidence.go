package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk/filesystem"
)

const evidencePreviewChars = 6000

type evidenceRecord struct {
	ID            string `json:"id"`
	Tool          string `json:"tool"`
	URL           string `json:"url,omitempty"`
	Query         string `json:"query,omitempty"`
	Title         string `json:"title,omitempty"`
	RetrievedAt   string `json:"retrieved_at"`
	Path          string `json:"path,omitempty"`
	Excerpt       string `json:"excerpt,omitempty"`
	body          string
	persistedHash [32]byte // Hash of exact file bytes; valid only when Path is set.
}

// evidenceStore records source identity and readable tool output together. The
// index is a durable catalog; search returns small excerpts from full records.
// It knows neither agent plans nor provider budgets.
type evidenceStore struct {
	mu             sync.Mutex
	backend        filesystem.Backend
	dir, indexPath string
	records        []evidenceRecord
}

func newEvidenceStore(backend filesystem.Backend, dir string) *evidenceStore {
	return &evidenceStore{backend: backend, dir: dir}
}

func (s *evidenceStore) capture(ctx context.Context, key, tool string, args map[string]any, body string) string {
	digest := sha256.Sum256([]byte(key))
	r := evidenceRecord{ID: hex.EncodeToString(digest[:8]), Tool: tool, RetrievedAt: time.Now().UTC().Format(time.RFC3339), body: body, Excerpt: trunc(body, 700)}
	r.URL, _ = args["url"].(string)
	r.Query, _ = args["query"].(string)
	// Source headers belong to the tool response, before the first blank line.
	// Do not interpret matching prose later in the page as provenance.
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			break
		}
		if strings.HasPrefix(line, "URL: ") {
			r.URL = strings.TrimPrefix(line, "URL: ")
		}
		if strings.HasPrefix(line, "Title: ") {
			r.Title = strings.TrimPrefix(line, "Title: ")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend != nil && s.dir != "" {
		path := filepath.Join(s.dir, r.ID+".txt")
		header := fmt.Sprintf("Source: %s\nTool: %s\nRetrieved: %s\n\n", r.URL, tool, r.RetrievedAt)
		if err := s.backend.Write(ctx, &filesystem.WriteRequest{FilePath: path, Content: header + body}); err == nil {
			r.Path = path
			r.persistedHash = sha256.Sum256([]byte(header + body))
		}
	}
	s.records = append(s.records, r)
	if r.Path != "" {
		// Keep the source inventory below ordinary tool previews. Source bodies
		// belong in their own files/search results, not in every catalog snapshot.
		catalog := append([]evidenceRecord(nil), s.records...)
		for i := range catalog {
			catalog[i].Excerpt = ""
		}
		raw, _ := json.Marshal(catalog)
		path := filepath.Join(s.dir, fmt.Sprintf("catalog-%03d.json", len(s.records)))
		if err := s.backend.Write(ctx, &filesystem.WriteRequest{FilePath: path, Content: string(raw)}); err == nil {
			s.indexPath = path
		}
		return fmt.Sprintf("[Evidence %s; retrieved %s; full text: %s]\n%s", r.ID, r.RetrievedAt, r.Path, trunc(body, evidencePreviewChars))
	}
	// Do not truncate the only accessible copy if persistence is unavailable.
	return body
}

func (s *evidenceStore) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := fmt.Sprintf("%d successful observations saved; query them with search_evidence.", len(s.records))
	if s.indexPath != "" {
		text += " Source catalog: " + s.indexPath
	}
	return text
}

func (s *evidenceStore) search(query string, limit int) []evidenceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	terms := strings.Fields(strings.ToLower(query))
	type match struct {
		r     evidenceRecord
		score int
	}
	hits := []match{}
	for _, r := range s.records {
		hay := strings.ToLower(r.ID + " " + r.URL + " " + r.Title + " " + r.Query + " " + r.body)
		score := 0
		for _, term := range terms {
			if strings.Contains(hay, term) {
				score++
			}
		}
		if len(terms) > 0 && score == 0 {
			continue
		}
		r.Excerpt = evidenceExcerpt(r.body, terms, 1000)
		hits = append(hits, match{r, score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]evidenceRecord, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.r)
	}
	return out
}

func evidenceExcerpt(body string, terms []string, limit int) string {
	text := []rune(body)
	lower := strings.ToLower(body)
	for _, term := range terms {
		if pos := strings.Index(lower, term); pos >= 0 {
			// Index in the lower-case string, then slice by runes to preserve UTF-8.
			start := len([]rune(lower[:pos])) - 120
			if start < 0 {
				start = 0
			}
			if start > len(text) {
				start = len(text)
			}
			return trunc(string(text[start:]), limit)
		}
	}
	return trunc(body, limit)
}

type evidenceSearchTool struct{ s *researchSession }

func (evidenceSearchTool) Name() string { return "search_evidence" }
func (evidenceSearchTool) Description() string {
	return "Search this run's already-read sources without network requests. Returns source URLs, retrieval dates, relevant excerpts and full-text file paths. Use before repeating searches or re-fetching pages. Empty query lists the first saved sources."
}
func (evidenceSearchTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "keywords, source URL, or evidence id"}, "limit": map[string]any{"type": "integer", "description": "maximum results, 1 to 10; default 5"}}}
}
func (t evidenceSearchTool) Invoke(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	q, _ := args["query"].(string)
	n := 5
	if v, ok := args["limit"].(float64); ok && v >= 1 && v <= 10 {
		n = int(v)
	}
	if v, ok := args["limit"].(int); ok && v >= 1 && v <= 10 {
		n = v
	}
	raw, err := json.Marshal(t.s.evidence.search(q, n))
	return string(raw), err
}

package service

import (
	"context"
	"strings"
	"testing"
)

func TestEvidencePreviewLabelsUnreadTailAndCanRetrieveIt(t *testing.T) {
	s := newResearchSession(newWorkspaceBackend(t.TempDir(), "owner", "conversation"), ".orka_offload/evidence", nil, 10)
	body := "Coverage: complete response body\n\n" + strings.Repeat("原始材料", 2000) + "distinctive_tail commenter only mentioned a related project."
	got := s.evidence.capture(context.Background(), "source", "http_request", map[string]any{"url": "https://example.test/source"}, body)
	if !strings.Contains(got, "preview only") || !strings.Contains(got, "not yet read") || strings.Contains(got, "distinctive_tail") {
		t.Fatal("preview misrepresented coverage", got[:min(len(got), 500)])
	}
	matches := s.evidence.search("distinctive_tail", 1)
	if len(matches) != 1 || !strings.Contains(matches[0].Excerpt, "only mentioned") {
		t.Fatal("targeted source passage inaccessible")
	}
}

// Asked about FastAPI, a researcher was shown the Flask PyPI record 40 times:
// both share field names like version and license, and the earlier record won
// every tie, so the passage it needed never surfaced.
func TestEvidenceSearchRanksTheNamedSourceAndExcerptsTheAskedField(t *testing.T) {
	s := newResearchSession(newWorkspaceBackend(t.TempDir(), "owner", "conversation"), ".orka_offload/evidence", nil, 10)
	pypi := func(name, version, license string) string {
		return `{"info":{"name":"` + name + `","summary":"` + strings.Repeat("description ", 200) + `","version":"` + version + `","license_expression":"` + license + `"}}`
	}
	s.evidence.capture(context.Background(), "flask", "fetch_url", map[string]any{"url": "https://pypi.org/pypi/flask/json"}, pypi("Flask", "3.1.3", "BSD-3-Clause"))
	s.evidence.capture(context.Background(), "fastapi", "fetch_url", map[string]any{"url": "https://pypi.org/pypi/fastapi/json"}, pypi("fastapi", "0.148.0", "MIT"))
	for _, query := range []string{"fastapi version license_expression", `"fastapi" "license_expression"`} {
		hits := s.evidence.search(query, 2)
		if len(hits) == 0 || !strings.Contains(hits[0].URL, "fastapi") {
			t.Fatalf("%s: top hit %+v", query, hits)
		}
		if !strings.Contains(hits[0].Excerpt, `"license_expression":"MIT"`) {
			t.Fatalf("%s: excerpt misses the asked field: %q", query, hits[0].Excerpt)
		}
	}
}

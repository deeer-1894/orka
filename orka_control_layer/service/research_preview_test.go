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

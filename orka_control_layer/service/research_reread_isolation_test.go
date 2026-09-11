package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/orka-oss/orka_core/toolargs"
)

func savedReadFixture(t *testing.T) (*researchSession, string, string, func() (string, error)) {
	t.Helper()
	base := t.TempDir()
	s := newResearchSession(newWorkspaceBackend(base, "reader"), ".orka_offload/reread/evidence", nil, 40)
	s.evidence.capture(context.Background(), "one", "fetch_url", map[string]any{"url": "https://example.test/source"}, strings.Repeat("Original evidence. ", 250)+"ORIGINAL_TAIL")
	path := s.evidence.records[0].Path
	diskPath := filepath.Join(base, "reader", path)
	read := func() (string, error) { b, err := os.ReadFile(diskPath); return string(b), err }
	return s, path, diskPath, read
}

func TestEditedEvidenceAlwaysStaysFullWithoutRecapture(t *testing.T) {
	s, path, diskPath, read := savedReadFixture(t)
	ctx := withResearchSession(context.Background(), s)
	original := s.evidence.records[0]
	changed := strings.Repeat("New introduction. ", 1000) + "UNIQUE_FRESH_SETTING=enabled"
	if err := os.WriteFile(diskPath, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := s.invoke(ctx, "file_read", map[string]any{"path": path}, read)
		if err != nil || got != changed {
			t.Fatalf("read %d shortened edited evidence: %.120s, %v", i, got, err)
		}
	}
	if len(s.evidence.records) != 1 || s.evidence.records[0] != original {
		t.Fatal("edited file mutated captured provenance")
	}
	if hits := s.evidence.search("UNIQUE_FRESH_SETTING", 5); len(hits) != 0 {
		t.Fatal("edited file was recaptured as remote evidence")
	}
}

func TestEvidenceFirstReadIsLocalToConsumer(t *testing.T) {
	s, path, _, read := savedReadFixture(t)
	body, _ := read()
	for i := 0; i < 2; i++ {
		ctx := withResearchSession(context.Background(), s)
		first, err := s.invoke(ctx, "file_read", map[string]any{"filename": path}, read)
		if err != nil || first != body {
			t.Fatalf("consumer %d lost its first full read", i)
		}
		repeat, err := s.invoke(ctx, "file_read", map[string]any{"path": path}, read)
		if err != nil || len([]rune(repeat)) > 1600 || !strings.Contains(repeat, "search_evidence") {
			t.Fatalf("consumer %d repeat was not bounded", i)
		}
	}
}

func TestGuidanceScopesParallelEvidenceReads(t *testing.T) {
	s, path, _, read := savedReadFixture(t)
	body, _ := read()
	root := withResearchSession(context.Background(), s)
	g := newResearchGuidance(root)
	var consumers []context.Context
	parent, _, err := g.BeforeAgent(root, &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatal(err)
	}
	consumers = append(consumers, parent)
	for i := 0; i < 4; i++ {
		child, _, err := g.BeforeAgent(parent, &adk.ChatModelAgentContext{})
		if err != nil {
			t.Fatal(err)
		}
		consumers = append(consumers, child)
	}
	var wg sync.WaitGroup
	for _, ctx := range consumers {
		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			first, err := s.invoke(ctx, "file_read", map[string]any{"file_path": path}, read)
			if err != nil || first != body {
				t.Error("parallel consumer lost its first full read")
			}
			var reads sync.WaitGroup
			for j := 0; j < 4; j++ {
				reads.Add(1)
				go func() {
					defer reads.Done()
					got, err := s.invoke(ctx, "file_read", map[string]any{"path": path}, read)
					if err != nil || !strings.Contains(got, "search_evidence") {
						t.Error("repeat not reduced")
					}
				}()
			}
			reads.Wait()
		}(ctx)
	}
	wg.Wait()
}

func TestEvidenceRereadKeepsUnderlyingErrors(t *testing.T) {
	s, path, _, read := savedReadFixture(t)
	ctx := withResearchSession(context.Background(), s)
	_, _ = s.invoke(ctx, "file_read", map[string]any{"path": path}, read)
	denied := errors.New("permission denied")
	got, err := s.invoke(ctx, "file_read", map[string]any{"path": path}, func() (string, error) { return "", denied })
	if !errors.Is(err, denied) || got != "" {
		t.Fatalf("permission failure hidden: %q %v", got, err)
	}
}

func rereadFileTool(read func() (string, error), path string) retrievalFixture {
	return retrievalFixture{"file_read", func(_ context.Context, args map[string]any) (string, error) {
		if toolargs.Path(args) != path {
			return "", errors.New("unexpected file path")
		}
		return read()
	}}

}

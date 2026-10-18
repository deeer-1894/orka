package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"
)

// A shared research session can serve several agents with separate histories.
// Keep read delivery history on each agent execution's context, not the store.
type evidenceReadTracker struct {
	mu    sync.Mutex
	reads map[string][32]byte
}

type evidenceReadTrackerKey struct{}

func withEvidenceReadTracker(ctx context.Context) context.Context {
	return context.WithValue(ctx, evidenceReadTrackerKey{}, &evidenceReadTracker{reads: make(map[string][32]byte)})
}

// reduceRepeatedRead runs AFTER the real file tool, preserving its permission
// checks, errors and freshness. Only unchanged original captures already read
// by this consumer are shortened. Edited files always stay whole: search_evidence
// still represents the original source and must not be offered as their fallback.
func (s *evidenceStore) reduceRepeatedRead(ctx context.Context, path, body string) string {
	tracker, _ := ctx.Value(evidenceReadTrackerKey{}).(*evidenceReadTracker)
	if tracker == nil {
		return body
	}
	path = filepath.Clean(path)
	digest := sha256.Sum256([]byte(body))
	s.mu.Lock()
	var original *evidenceRecord
	for _, record := range s.records {
		if record.Path != "" && path == record.Path && record.persistedHash == digest {
			copy := record
			original = &copy
			break
		}
	}
	s.mu.Unlock()
	if original == nil {
		return body
	}
	tracker.mu.Lock()
	previous, seen := tracker.reads[path]
	tracker.reads[path] = digest
	tracker.mu.Unlock()
	if !seen || previous != digest {
		return body
	}
	return fmt.Sprintf("[unchanged evidence already read by this agent; id: %s; source: %s]\nThis file has already been returned in full in this agent execution. Use search_evidence with specific keywords or this evidence ID for a passage; write the sourced findings and continue the remaining deliverables instead of reloading the same source.\n%s", original.ID, original.URL, trunc(body, 1000))
}

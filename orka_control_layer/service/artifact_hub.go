package service

import "sync"

// ArtifactHub is a tiny in-process pub/sub. The artifact tool publishes a
// version bump when an artifact is (re)published; the SSE endpoint subscribes
// per artifact and pushes the new version number to every open viewer, which
// then refetches and re-renders in place. Survives the process; not durable
// (a missed event is recovered by the viewer's reconnect refetch).
type artifactHub struct {
	mu   sync.Mutex
	subs map[string]map[chan int]struct{} // artifact_id -> subscriber channels
}

// ArtifactHub is the process-wide instance (set once, used by tool + API).
var ArtifactHub = &artifactHub{subs: map[string]map[chan int]struct{}{}}

// Publish notifies every subscriber of an artifact that a new version exists.
func (h *artifactHub) Publish(artifactID string, version int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[artifactID] {
		select {
		case ch <- version:
		default: // slow consumer — drop; its next refetch reconciles
		}
	}
}

// Subscribe returns a channel of version numbers for an artifact and a cancel
// func that unsubscribes and closes it.
func (h *artifactHub) Subscribe(artifactID string) (<-chan int, func()) {
	ch := make(chan int, 4)
	h.mu.Lock()
	if h.subs[artifactID] == nil {
		h.subs[artifactID] = map[chan int]struct{}{}
	}
	h.subs[artifactID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if m := h.subs[artifactID]; m != nil {
			delete(m, ch)
			if len(m) == 0 {
				delete(h.subs, artifactID)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
}

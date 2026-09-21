package browsertool

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

const (
	navigationFailureTTL  = 30 * time.Minute
	maxNavigationFailures = 2048
)

// navigationRetryGuard rejects an exact repeat of a deterministic navigation
// failure within one run. Alternate official URLs remain available, and state
// never crosses run boundaries.
type navigationRetryGuard struct {
	mu       sync.Mutex
	failures map[navigationFailureKey]navigationFailure
	now      func() time.Time
}

type navigationFailureKey struct {
	ownerID        string
	conversationID string
	runID          string
	url            string
}

type navigationFailure struct {
	code string
	at   time.Time
}

func newNavigationRetryGuard() *navigationRetryGuard {
	return &navigationRetryGuard{
		failures: make(map[navigationFailureKey]navigationFailure),
		now:      time.Now,
	}
}

func (g *navigationRetryGuard) blocked(identity connectors.GUIIdentity, req Request) *ActionError {
	if g == nil || req.Action != "open" {
		return nil
	}
	key := navigationKey(identity, req.URL)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	g.pruneLocked(now)
	failure, ok := g.failures[key]
	if !ok {
		return nil
	}
	return NewActionError(
		"navigation_retry_blocked",
		"This URL already failed with "+failure.code+" in the current run; use a different official endpoint or report it unavailable.",
	)
}

func (g *navigationRetryGuard) record(identity connectors.GUIIdentity, req Request, result Result) {
	if g == nil || req.Action != "open" {
		return
	}
	key := navigationKey(identity, req.URL)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	g.pruneLocked(now)
	if result.OK {
		delete(g.failures, key)
		return
	}
	if result.Error == nil || !deterministicNavigationFailure(result.Error.Code) {
		return
	}
	if len(g.failures) >= maxNavigationFailures {
		g.removeOldestLocked()
	}
	g.failures[key] = navigationFailure{code: result.Error.Code, at: now}
}

func deterministicNavigationFailure(code string) bool {
	return code == "navigation_network" || code == "navigation_certificate"
}

func navigationKey(identity connectors.GUIIdentity, rawURL string) navigationFailureKey {
	return navigationFailureKey{
		ownerID:        identity.OwnerID,
		conversationID: identity.ConversationID,
		runID:          identity.RunID,
		url:            normalizedNavigationURL(rawURL),
	}
}

func normalizedNavigationURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed.String()
}

func (g *navigationRetryGuard) pruneLocked(now time.Time) {
	for key, failure := range g.failures {
		if now.Sub(failure.at) >= navigationFailureTTL {
			delete(g.failures, key)
		}
	}
}

func (g *navigationRetryGuard) removeOldestLocked() {
	var oldestKey navigationFailureKey
	var oldestTime time.Time
	for key, failure := range g.failures {
		if oldestTime.IsZero() || failure.at.Before(oldestTime) {
			oldestKey, oldestTime = key, failure.at
		}
	}
	if !oldestTime.IsZero() {
		delete(g.failures, oldestKey)
	}
}

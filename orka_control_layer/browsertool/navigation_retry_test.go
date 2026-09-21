package browsertool

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_control_layer/connectors"
)

func TestNavigationRetryGuardBlocksExactRepeatWithinRun(t *testing.T) {
	lease := &fixtureLease{handler: func(method string, _ any, out any) (bool, error) {
		if method != "Page.navigate" {
			return false, nil
		}
		raw, _ := json.Marshal(map[string]any{"errorText": "net::ERR_CONNECTION_TIMED_OUT"})
		return true, json.Unmarshal(raw, out)
	}}
	dialer := &fixtureDialer{lease: lease}
	browser := New(dialer, t.TempDir())
	ctx := browserTestContext()

	first, _ := browser.Invoke(ctx, map[string]any{"action": "open", "url": "https://go.dev/dl/#stable"})
	second, _ := browser.Invoke(ctx, map[string]any{"action": "open", "url": "https://GO.DEV/dl/"})
	alternate, _ := browser.Invoke(ctx, map[string]any{"action": "open", "url": "https://golang.org/dl/"})

	if !strings.Contains(first, `"code":"navigation_network"`) {
		t.Fatalf("first failure = %s", first)
	}
	if !strings.Contains(second, `"code":"navigation_retry_blocked"`) {
		t.Fatalf("repeat was not blocked = %s", second)
	}
	if !strings.Contains(alternate, `"code":"navigation_network"`) || dialer.acquires != 2 {
		t.Fatalf("alternate endpoint was blocked: acquires=%d result=%s", dialer.acquires, alternate)
	}

	otherRun := connectors.WithGUIIdentity(ctx, connectors.GUIIdentity{OwnerID: "owner", ConversationID: "conversation", RunID: "other-run"})
	third, _ := browser.Invoke(otherRun, map[string]any{"action": "open", "url": "https://go.dev/dl/"})
	if !strings.Contains(third, `"code":"navigation_network"`) || dialer.acquires != 3 {
		t.Fatalf("failure leaked across runs: acquires=%d result=%s", dialer.acquires, third)
	}
}

func TestNavigationRetryGuardExpiresFailures(t *testing.T) {
	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	guard := newNavigationRetryGuard()
	guard.now = func() time.Time { return now }
	identity := connectors.GUIIdentity{OwnerID: "owner", ConversationID: "conversation", RunID: "run"}
	req := Request{Action: "open", URL: "https://example.com/download#latest"}
	guard.record(identity, req, Result{Error: NewActionError("navigation_network", "failed")})
	if guard.blocked(identity, Request{Action: "open", URL: "https://EXAMPLE.com/download"}) == nil {
		t.Fatal("normalized repeat was not blocked")
	}
	now = now.Add(navigationFailureTTL)
	if guard.blocked(identity, req) != nil {
		t.Fatal("expired failure still blocked")
	}
}

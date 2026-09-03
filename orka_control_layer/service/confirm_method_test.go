package service

import "testing"

// The gate is a name list, but http_request covers both a read and a write. It
// fired on `GET https://pkg.go.dev/...` mid-research — a public docs fetch,
// identical in effect to fetch_url, which is not gated at all.
func TestNeedsConfirmOnlyForMutatingHTTP(t *testing.T) {
	safe := []map[string]any{
		{"url": "https://pkg.go.dev/x"}, // method omitted = the documented GET default
		{"url": "https://pkg.go.dev/x", "method": "GET"},
		{"url": "https://pkg.go.dev/x", "method": "get"}, // models do not match our casing
		{"url": "https://pkg.go.dev/x", "method": " Get "},
		{"url": "https://pkg.go.dev/x", "method": "HEAD"},
	}
	for _, a := range safe {
		if needsConfirm("http_request", a) {
			t.Errorf("a read should not need approval: %v", a)
		}
	}

	for _, m := range []string{"POST", "post", "PUT", "PATCH", "DELETE"} {
		if !needsConfirm("http_request", map[string]any{"url": "https://x", "method": m}) {
			t.Errorf("%s can change remote state and must stay gated", m)
		}
	}
	// A non-string method is a malformed call, not a licence to skip the gate.
	if !needsConfirm("http_request", map[string]any{"method": 42}) {
		t.Error("a malformed method must fall back to asking")
	}
}

// Narrowing http_request must not narrow anything else: the rest of the list
// runs arbitrary code or commits a consequential change however it is called.
func TestNeedsConfirmUnchangedForOtherTools(t *testing.T) {
	for name := range dangerTools {
		if name == "http_request" {
			continue
		}
		if !needsConfirm(name, map[string]any{"method": "GET"}) {
			t.Errorf("%q must stay gated regardless of its args", name)
		}
	}
}

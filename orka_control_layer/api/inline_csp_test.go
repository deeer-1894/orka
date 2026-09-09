package api

import (
	"strings"
	"testing"
)

// Rendered HTML is model-generated code executing on THIS app's origin — the
// origin whose localStorage holds the session token. Sandboxing it is what stops
// a generated page from reading that token, and the absence of
// allow-same-origin is the entire mechanism.
func TestRenderedHTMLIsSandboxed(t *testing.T) {
	got := inlineCSP("inline", "text/html; charset=utf-8")
	if !strings.Contains(got, "sandbox") {
		t.Fatalf("policy = %q, want a sandbox", got)
	}
	// allow-same-origin would hand the document our origin back and undo all of it.
	if strings.Contains(got, "allow-same-origin") {
		t.Fatalf("policy = %q — allow-same-origin defeats the sandbox", got)
	}
	// Scripts must still run, or an animated page renders as a still frame.
	if !strings.Contains(got, "allow-scripts") {
		t.Fatalf("policy = %q — scripts are blocked, so animations would not play", got)
	}
}

// A download is saved, not executed, so it needs no policy; adding one to every
// response would be noise.
func TestADownloadedFileGetsNoPolicy(t *testing.T) {
	if got := inlineCSP("attachment", "text/html; charset=utf-8"); got != "" {
		t.Fatalf("policy = %q on an attachment", got)
	}
}

// Only HTML executes. A PDF or an image rendered inline is inert.
func TestNonHTMLNeedsNoPolicy(t *testing.T) {
	for _, ct := range []string{"application/pdf", "image/png", "text/markdown; charset=utf-8", "text/plain"} {
		if got := inlineCSP("inline", ct); got != "" {
			t.Errorf("%s got policy %q", ct, got)
		}
	}
}

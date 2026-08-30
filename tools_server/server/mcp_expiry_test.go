package server

import (
	"strings"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/security"
)

// An EXPIRED token used to fall through to the unauthenticated branch, yielding
// an identity with no scopes — so every tool answered "permission denied:
// missing scope file:write". That message sent the model, and whoever debugged
// it, after a permissions problem that did not exist. Measured on the live
// deployment, 31 of 44 such denials landed past the 30-minute token TTL.
func TestExpiredTokenReportsAuthNotPermission(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})

	// Built directly rather than via NewToken: that helper treats a non-positive
	// TTL as "never expires", so it cannot express the state under test.
	expired, err := security.Sign(security.ContextToken{
		UserEmail: "u@x.com",
		Scopes:    []string{"file:read", "file:write"},
		Exp:       time.Now().Add(-time.Minute).Unix(),
	}, []byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, url, map[string]string{"X-Orka-Token": expired})
	_, txt := callText(t, c, "file_write", map[string]any{"path": "a.txt", "content": "x"})

	if strings.Contains(txt, "missing scope") {
		t.Fatalf("an expired token was reported as a permission problem: %s", txt)
	}
	if !strings.Contains(txt, "auth failed") {
		t.Fatalf("expected an authentication error, got: %s", txt)
	}
}

// A genuine scope denial must still say so — the fix above must not turn every
// refusal into an auth error.
func TestMissingScopeStillReportsPermission(t *testing.T) {
	base := t.TempDir()
	url := startGateway(t, Config{Secret: testSecret, BaseStorage: base})
	c := connect(t, url, tokenHeader(t, "u@x.com", []string{"file:read"})) // no file:write
	_, txt := callText(t, c, "file_write", map[string]any{"path": "a.txt", "content": "x"})

	if !strings.Contains(txt, "missing scope") {
		t.Fatalf("a real denial no longer reports as one: %s", txt)
	}
}

package api

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func runAuth(a *API, method, header, query string) (int, string) {
	c := app.NewContext(0)
	c.Request.Header.SetMethod(method)
	uri := "/api/v1/controller/file/download"
	if query != "" {
		uri += "?token=" + query
	}
	c.Request.SetRequestURI(uri)
	if header != "" {
		c.Request.Header.Set("Authorization", "Bearer "+header)
	}
	a.AuthMiddleware()(context.Background(), c)
	return c.Response.StatusCode(), authEmail(c)
}

// A session lives only in the Authorization header; URLs carry a short-lived
// media token that works for GET only; and bumping the account's token version
// revokes everything issued before.
func TestSessionAndMediaTokensAndRevocation(t *testing.T) {
	a := &API{Secret: "test-secret"}
	ctx := context.Background()
	session, _ := a.issueToken(ctx, "a@x")
	media, _ := a.sign(ctx, "a@x", scopeMedia, mediaTTL)

	if code, who := runAuth(a, "POST", session, ""); code != 200 || who != "a@x" {
		t.Fatalf("session in header: %d %q", code, who)
	}
	if code, _ := runAuth(a, "GET", "", session); code != 401 {
		t.Fatalf("a session token in a URL must be refused, got %d", code)
	}
	if code, who := runAuth(a, "GET", "", media); code != 200 || who != "a@x" {
		t.Fatalf("media token for a GET download: %d %q", code, who)
	}
	if code, _ := runAuth(a, "POST", "", media); code != 401 {
		t.Fatalf("a media token must not authorize a POST, got %d", code)
	}
	if code, _ := runAuth(a, "POST", media, ""); code != 401 {
		t.Fatalf("a media token must not work as a session, got %d", code)
	}

	a.rememberVersion("a@x", 1) // logout or password change
	if code, _ := runAuth(a, "POST", session, ""); code != 401 {
		t.Fatalf("a revoked session must be refused, got %d", code)
	}
	if code, _ := runAuth(a, "GET", "", media); code != 401 {
		t.Fatalf("a revoked media token must be refused, got %d", code)
	}
	fresh, _ := a.issueToken(ctx, "a@x")
	if code, _ := runAuth(a, "POST", fresh, ""); code != 200 {
		t.Fatalf("a token issued after the bump works, got %d", code)
	}
}

func TestNewPasswordsNeedLengthLettersAndDigits(t *testing.T) {
	for _, bad := range []string{"123456", "abcdefgh", "12345678", "ab12"} {
		if validPassword(bad) == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	for _, good := range []string{"orca2026", "库存管理stock8"} {
		if err := validPassword(good); err != nil {
			t.Errorf("%q refused: %v", good, err)
		}
	}
}

package security

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("s3cr3t")
	tok := NewToken("a@b.com", []string{"file:read", "file:write"}, time.Hour)
	signed, err := Sign(tok, secret)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(signed, secret)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserEmail != "a@b.com" {
		t.Fatalf("email = %s", got.UserEmail)
	}
	if !got.HasScope("file:read") || got.HasScope("lark:write") {
		t.Fatalf("scope check wrong: %+v", got.Scopes)
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	secret := []byte("s3cr3t")
	signed, _ := Sign(NewToken("a@b.com", []string{"x"}, time.Hour), secret)
	// flip a byte in the payload portion
	b := []byte(signed)
	b[0] ^= 0xFF
	if _, err := Verify(string(b), secret); err != ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	// wrong secret
	if _, err := Verify(signed, []byte("other")); err != ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken for wrong secret, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	secret := []byte("s3cr3t")
	tok := ContextToken{UserEmail: "a@b.com", Exp: time.Now().Add(-time.Second).Unix()}
	signed, _ := Sign(tok, secret)
	if _, err := Verify(signed, secret); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestWildcardScope(t *testing.T) {
	tok := ContextToken{Scopes: []string{"*"}}
	if !tok.HasScope("anything") {
		t.Fatal("wildcard should grant any scope")
	}
}

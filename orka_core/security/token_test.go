package security

import (
	"strings"
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

// The conversation claim decides which directory the file tools may touch, so
// it has to survive the round trip and be covered by the signature. A caller
// able to change it after signing could read another conversation's files.
func TestConversationSurvivesTheRoundTrip(t *testing.T) {
	secret := []byte("s3cr3t")
	tok, err := Sign(NewToken("u@x.com", []string{"file:read"}, time.Minute).InConversation("conv-a"), secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := Verify(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Conversation != "conv-a" {
		t.Fatalf("conversation = %q, want conv-a", got.Conversation)
	}
}

func TestTamperingWithTheConversationIsRejected(t *testing.T) {
	secret := []byte("s3cr3t")
	tok, _ := Sign(NewToken("u@x.com", nil, time.Minute).InConversation("conv-a"), secret)
	// Re-sign the same claims with a different conversation under a DIFFERENT
	// secret: the payload is valid JSON, so only the signature can catch it.
	forged, _ := Sign(NewToken("u@x.com", nil, time.Minute).InConversation("conv-b"), []byte("other"))
	if _, err := Verify(forged, secret); err == nil {
		t.Fatal("a token signed with another secret was accepted")
	}
	// Splicing the forged payload onto the real signature must also fail.
	spliced := strings.Split(forged, ".")[0] + "." + strings.Split(tok, ".")[1]
	if _, err := Verify(spliced, secret); err == nil {
		t.Fatal("a spliced payload was accepted; conversation is not covered by the signature")
	}
}

// A token with no conversation must stay valid: scheduled tasks and the quant
// pipeline sign one, and they work in the account root by design.
func TestATokenWithoutAConversationIsStillValid(t *testing.T) {
	secret := []byte("s3cr3t")
	tok, _ := Sign(NewToken("u@x.com", []string{"*"}, time.Minute), secret)
	got, err := Verify(tok, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Conversation != "" {
		t.Fatalf("conversation = %q, want empty", got.Conversation)
	}
}

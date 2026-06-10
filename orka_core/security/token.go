// Package security implements signed context tokens. The control-layer gateway
// signs a short-lived token carrying identity + capability scopes; downstream
// services (tools_server) verify the signature before trusting identity, rather
// than blindly trusting plaintext headers.
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Errors.
var (
	ErrInvalidToken = errors.New("security: invalid token")
	ErrExpired      = errors.New("security: token expired")
)

// ContextToken is the signed identity + capability claim.
type ContextToken struct {
	UserEmail string   `json:"user_email"`
	Scopes    []string `json:"scopes"` // capability allowlist (RBAC)
	Exp       int64    `json:"exp"`    // unix seconds; 0 = no expiry
}

// NewToken builds a token valid for ttl from now.
func NewToken(email string, scopes []string, ttl time.Duration) ContextToken {
	exp := int64(0)
	if ttl > 0 {
		exp = time.Now().Add(ttl).Unix()
	}
	return ContextToken{UserEmail: email, Scopes: scopes, Exp: exp}
}

// Sign returns "<payload>.<hmac>" using HMAC-SHA256 over the payload.
func Sign(t ContextToken, secret []byte) (string, error) {
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + mac(p, secret), nil
}

// Verify checks the signature and expiry and returns the claims.
func Verify(token string, secret []byte) (ContextToken, error) {
	var ct ContextToken
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return ct, ErrInvalidToken
	}
	if !hmac.Equal([]byte(mac(p, secret)), []byte(sig)) {
		return ct, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return ct, ErrInvalidToken
	}
	if err := json.Unmarshal(payload, &ct); err != nil {
		return ct, ErrInvalidToken
	}
	if ct.Exp > 0 && time.Now().Unix() > ct.Exp {
		return ct, ErrExpired
	}
	return ct, nil
}

// HasScope reports whether the token grants scope (wildcard "*" allows all).
func (t ContextToken) HasScope(scope string) bool {
	for _, s := range t.Scopes {
		if s == "*" || s == scope {
			return true
		}
	}
	return false
}

func mac(msg string, secret []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

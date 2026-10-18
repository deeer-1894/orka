// Package identity carries the verified caller identity through the request
// context. The gateway trusts identity only after verifying a signed context
// token (see server.ContextFunc) — never raw headers alone.
package identity

import (
	"context"

	"github.com/orka-oss/orka_core/pathsafe"
)

type ctxKey int

const idKey ctxKey = iota

// Identity is the verified caller: email plus granted capability scopes.
type Identity struct {
	ConversationID string
	Email          string
	Scopes         []string
	// AuthErr records why a PRESENTED token was rejected (expired, tampered).
	// It is what separates "this caller may not do that" from "we could not tell
	// who this caller is" — two states that used to look identical downstream,
	// so an expired token was reported as a missing permission.
	AuthErr error
}

// HasScope reports whether the identity grants scope (wildcard "*" allows all).
func (i Identity) HasScope(scope string) bool {
	for _, s := range i.Scopes {
		if s == "*" || s == scope {
			return true
		}
	}
	return false
}

// Root returns the conversation workspace this caller's file tools work in,
// creating it if needed. A caller without a valid conversation gets an error
// rather than the account root, so one run can never see another's files.
func (i Identity) Root(base string) (string, error) {
	return pathsafe.EnsureSession(base, i.Email, i.ConversationID)
}

// With stores an identity on the context.
func With(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, idKey, id)
}

// From extracts the identity ("" / no scopes if absent).
func From(ctx context.Context) Identity {
	if v, ok := ctx.Value(idKey).(Identity); ok {
		return v
	}
	return Identity{}
}

// CatalogOnly identifies the explicit metadata credential, not wildcard grants.
// It cannot invoke even otherwise scopeless tools, with or without a conversation.
func (i Identity) CatalogOnly() bool {
	for _, scope := range i.Scopes {
		if scope == "tools:catalog" {
			return true
		}
	}
	return false
}

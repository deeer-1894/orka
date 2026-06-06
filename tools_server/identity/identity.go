// Package identity carries the verified caller identity through the request
// context. The gateway trusts identity only after verifying a signed context
// token (see server.ContextFunc) — never raw headers alone.
package identity

import "context"

type ctxKey int

const idKey ctxKey = iota

// Identity is the verified caller: email plus granted capability scopes.
type Identity struct {
	Email  string
	Scopes []string
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

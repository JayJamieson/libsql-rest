// Package auth carries the authenticated caller through the request lifecycle.
// The Principal placed in the request context is the seam future row-level
// security will read from: handlers and the store can retrieve the current user
// via FromContext without any change to their signatures.
package auth

import "context"

// Principal describes the caller behind a request. An unauthenticated request
// carries an anonymous Principal (Subject == "") rather than nil, so consumers
// never have to nil-check.
type Principal struct {
	// Subject is the authenticated user identifier (the JWT `sub` claim).
	Subject string
	// Claims holds every claim from the token, so authorization rules can
	// reference arbitrary fields (roles, tenant, email, ...). It is nil for
	// anonymous principals.
	Claims map[string]any
}

// Anonymous returns a non-authenticated principal.
func Anonymous() *Principal { return &Principal{} }

// IsAuthenticated reports whether the principal represents a real user.
func (p *Principal) IsAuthenticated() bool {
	return p != nil && p.Subject != ""
}

// UserID returns the caller's user identifier — the JWT `sub` claim. This is
// the value row-level security rules reference (PocketBase's `@request.auth.id`).
// It is empty for anonymous callers.
func (p *Principal) UserID() string {
	if p == nil {
		return ""
	}
	return p.Subject
}

// Claim returns the raw value of a claim by name.
func (p *Principal) Claim(name string) (any, bool) {
	if p == nil || p.Claims == nil {
		return nil, false
	}
	v, ok := p.Claims[name]
	return v, ok
}

// ClaimString returns a claim as a string, or "" if absent or not a string.
func (p *Principal) ClaimString(name string) string {
	if v, ok := p.Claim(name); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ctxKey is an unexported context key so only this package can set the principal.
type ctxKey struct{}

// WithPrincipal returns a copy of ctx carrying the given principal.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal stored in ctx, or an anonymous principal if
// none is present. It never returns nil.
func FromContext(ctx context.Context) *Principal {
	if p, ok := ctx.Value(ctxKey{}).(*Principal); ok && p != nil {
		return p
	}
	return Anonymous()
}

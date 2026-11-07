// Package auth validates JWT bearer tokens and stores the caller in the request context.
package auth

import "context"

type Principal struct {
	Subject string
	Claims  map[string]any
}

func Anonymous() *Principal { return &Principal{} }

func (p *Principal) IsAuthenticated() bool {
	return p != nil && p.Subject != ""
}

func (p *Principal) Claim(name string) (any, bool) {
	if p == nil || p.Claims == nil {
		return nil, false
	}
	v, ok := p.Claims[name]
	return v, ok
}

func (p *Principal) ClaimString(name string) string {
	if v, ok := p.Claim(name); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) *Principal {
	if p, ok := ctx.Value(ctxKey{}).(*Principal); ok && p != nil {
		return p
	}
	return Anonymous()
}

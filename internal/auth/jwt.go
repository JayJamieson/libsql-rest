package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	jwtmiddleware "github.com/auth0/go-jwt-middleware/v2"
	"github.com/auth0/go-jwt-middleware/v2/validator"
)

type JWTOptions struct {
	Algorithm           string
	HMACSecret          []byte
	RSAPublicKey        *rsa.PublicKey
	Issuer              string
	Audience            []string
	CredentialsOptional bool
}

// The validator rejects any token whose header names a different algorithm
// from the configured one. That stops an attacker from signing an HS256 token
// with the server's RSA public key as the secret.
var supportedAlgorithms = map[string]validator.SignatureAlgorithm{
	"HS256": validator.HS256,
	"HS384": validator.HS384,
	"HS512": validator.HS512,
	"RS256": validator.RS256,
	"RS384": validator.RS384,
	"RS512": validator.RS512,
}

func NewJWTMiddleware(opts JWTOptions) (func(http.Handler) http.Handler, error) {
	alg := strings.ToUpper(opts.Algorithm)
	sigAlg, ok := supportedAlgorithms[alg]
	if !ok {
		return nil, fmt.Errorf("unsupported jwt algorithm %q (supported: HS256/384/512, RS256/384/512)", opts.Algorithm)
	}

	keyFunc, err := keyFuncFor(alg, opts)
	if err != nil {
		return nil, err
	}

	jwtValidator, err := validator.New(
		keyFunc,
		sigAlg,
		opts.Issuer,
		opts.Audience,
		validator.WithCustomClaims(func() validator.CustomClaims { return &mapClaims{} }),
	)
	if err != nil {
		return nil, fmt.Errorf("configuring jwt validator: %w", err)
	}

	mw := jwtmiddleware.New(
		jwtValidator.ValidateToken,
		jwtmiddleware.WithCredentialsOptional(opts.CredentialsOptional),
		jwtmiddleware.WithErrorHandler(errorHandler),
	)

	return func(next http.Handler) http.Handler {
		return mw.CheckJWT(principalAdapter(next))
	}, nil
}

func keyFuncFor(alg string, opts JWTOptions) (func(context.Context) (any, error), error) {
	switch {
	case strings.HasPrefix(alg, "HS"):
		if len(opts.HMACSecret) == 0 {
			return nil, fmt.Errorf("%s requires an HMAC secret", alg)
		}
		secret := opts.HMACSecret
		return func(context.Context) (any, error) { return secret, nil }, nil
	case strings.HasPrefix(alg, "RS"):
		if opts.RSAPublicKey == nil {
			return nil, fmt.Errorf("%s requires an RSA public key", alg)
		}
		pub := opts.RSAPublicKey
		return func(context.Context) (any, error) { return pub, nil }, nil
	default:
		return nil, fmt.Errorf("unsupported jwt algorithm %q", alg)
	}
}

func principalAdapter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := Anonymous()
		if claims, ok := r.Context().Value(jwtmiddleware.ContextKey{}).(*validator.ValidatedClaims); ok {
			principal = principalFromClaims(claims)
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

func principalFromClaims(claims *validator.ValidatedClaims) *Principal {
	p := &Principal{Subject: claims.RegisteredClaims.Subject}
	if mc, ok := claims.CustomClaims.(*mapClaims); ok {
		p.Claims = mc.fields
	}
	return p
}

func errorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	msg := "missing or invalid authorization token"
	if errors.Is(err, jwtmiddleware.ErrJWTMissing) {
		msg = "authorization token required"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type mapClaims struct {
	fields map[string]any
}

// Validate requires exp. go-jwt-middleware checks exp only when a token has
// one, so a token without it would never expire.
func (c *mapClaims) Validate(context.Context) error {
	if _, ok := c.fields["exp"]; !ok {
		return errors.New("token has no exp claim")
	}
	return nil
}

func (c *mapClaims) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &c.fields)
}

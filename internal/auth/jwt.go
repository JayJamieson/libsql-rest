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

// Middleware is a standard net/http middleware constructor.
type Middleware func(http.Handler) http.Handler

// JWTOptions configures the JWT middleware. It supports HMAC (HS256/384/512)
// with a shared secret and RSA (RS256/384/512) with a public key. RSA is the
// recommended production mode: the server holds only the public key and can
// verify tokens signed elsewhere without being able to mint them.
type JWTOptions struct {
	// Algorithm is the expected signing algorithm (default HS256).
	Algorithm string
	// HMACSecret is the shared signing key for HS* algorithms.
	HMACSecret []byte
	// RSAPublicKey is the verification key for RS* algorithms.
	RSAPublicKey *rsa.PublicKey
	// Issuer and Audience are validated against the token's `iss` and `aud`.
	Issuer   string
	Audience []string
	// CredentialsOptional lets unauthenticated requests through as anonymous
	// instead of being rejected with 401. This is the toggle row-level security
	// will use so per-table rules — not the middleware — decide access.
	CredentialsOptional bool
}

// supportedAlgorithms maps algorithm names to the validator's type. Pinning the
// expected algorithm here is also a security control: a token whose header
// advertises a different algorithm is rejected, preventing algorithm-confusion
// attacks (e.g. an RS256 public key being abused as an HS256 secret).
var supportedAlgorithms = map[string]validator.SignatureAlgorithm{
	"HS256": validator.HS256,
	"HS384": validator.HS384,
	"HS512": validator.HS512,
	"RS256": validator.RS256,
	"RS384": validator.RS384,
	"RS512": validator.RS512,
}

// NewJWTMiddleware builds middleware that validates a bearer JWT and attaches
// the resulting Principal to the request context. On validation failure it
// responds 401 with the API's standard JSON error envelope.
func NewJWTMiddleware(opts JWTOptions) (Middleware, error) {
	alg := strings.ToUpper(opts.Algorithm)
	if alg == "" {
		alg = "HS256"
	}
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
		// CheckJWT validates and stores its own claims in the context; the
		// adapter translates those into our Principal for downstream use.
		return mw.CheckJWT(principalAdapter(next))
	}, nil
}

// keyFuncFor returns the verification-key function for the given algorithm,
// validating that the matching key material was supplied.
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

// principalAdapter reads the library's validated claims from the context,
// converts them into a Principal, and stores that Principal for handlers/store.
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

// errorHandler renders JWT failures using the same envelope as the rest of the
// API ({"error": "..."}) with a 401 status.
func errorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	msg := "missing or invalid authorization token"
	if errors.Is(err, jwtmiddleware.ErrJWTMissing) {
		msg = "authorization token required"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// mapClaims captures the full JWT payload as a map so authorization rules can
// reference any claim. jose deserializes the payload JSON into this type via
// UnmarshalJSON.
type mapClaims struct {
	fields map[string]any
}

// Validate satisfies validator.CustomClaims; per-claim checks are deferred to
// the authorization layer.
func (c *mapClaims) Validate(context.Context) error { return nil }

func (c *mapClaims) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &c.fields)
}

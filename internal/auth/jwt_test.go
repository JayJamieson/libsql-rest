package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testSecret   = "super-secret-signing-key"
	testIssuer   = "libsql-rest-test"
	testAudience = "libsql-rest-test"
)

func signToken(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": testIssuer,
		"aud": testAudience,
		"sub": "user-123",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func captureHandler(seen **auth.Principal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.FromContext(r.Context())
		*seen = p
		w.WriteHeader(http.StatusOK)
	})
}

func newMW(t *testing.T, optional bool) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := auth.NewJWTMiddleware(auth.JWTOptions{
		Algorithm:           "HS256",
		HMACSecret:          []byte(testSecret),
		Issuer:              testIssuer,
		Audience:            []string{testAudience},
		CredentialsOptional: optional,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mw
}

func request(h http.Handler, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/tables", nil)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestValidTokenAttachesPrincipal(t *testing.T) {
	var seen *auth.Principal
	h := newMW(t, false)(captureHandler(&seen))

	token := signToken(t, func(c jwt.MapClaims) { c["role"] = "admin" })
	rec := request(h, token)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if seen == nil || !seen.IsAuthenticated() {
		t.Fatalf("principal not authenticated: %+v", seen)
	}
	if seen.Subject != "user-123" {
		t.Errorf("subject = %q", seen.Subject)
	}
	if seen.ClaimString("role") != "admin" {
		t.Errorf("role claim = %q, claims=%v", seen.ClaimString("role"), seen.Claims)
	}
}

func TestMissingTokenRejectedWhenRequired(t *testing.T) {
	var seen *auth.Principal
	h := newMW(t, false)(captureHandler(&seen))

	rec := request(h, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if seen != nil {
		t.Error("handler should not run without a token")
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Errorf("unexpected error body: %s", rec.Body)
	}
}

func TestInvalidAndExpiredTokensRejected(t *testing.T) {
	h := newMW(t, false)(captureHandler(new(*auth.Principal)))

	cases := map[string]string{
		"wrong signature": func() string {
			s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"iss": testIssuer, "aud": testAudience, "sub": "x",
				"exp": time.Now().Add(time.Hour).Unix(),
			}).SignedString([]byte("the-wrong-secret"))
			return s
		}(),
		"expired":        signToken(t, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"wrong issuer":   signToken(t, func(c jwt.MapClaims) { c["iss"] = "someone-else" }),
		"wrong audience": signToken(t, func(c jwt.MapClaims) { c["aud"] = "someone-else" }),
		"garbage":        "not-a-jwt",
		"no expiry":      signToken(t, func(c jwt.MapClaims) { delete(c, "exp") }),
	}
	for name, token := range cases {
		if rec := request(h, token); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
}

func TestOptionalCredentialsAllowAnonymous(t *testing.T) {
	var seen *auth.Principal
	h := newMW(t, true)(captureHandler(&seen))

	rec := request(h, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if seen == nil || seen.IsAuthenticated() {
		t.Errorf("expected anonymous principal, got %+v", seen)
	}

	if rec := request(h, "garbage"); rec.Code != http.StatusUnauthorized {
		t.Errorf("invalid token with optional creds: status = %d, want 401", rec.Code)
	}
}

func TestNewJWTMiddlewareValidation(t *testing.T) {
	base := auth.JWTOptions{Algorithm: "HS256", Issuer: testIssuer, Audience: []string{testAudience}}

	if _, err := auth.NewJWTMiddleware(base); err == nil {
		t.Error("expected error for missing HMAC secret")
	}
	unsupported := base
	unsupported.HMACSecret, unsupported.Algorithm = []byte("x"), "ZZ999"
	if _, err := auth.NewJWTMiddleware(unsupported); err == nil {
		t.Error("expected error for unsupported algorithm")
	}
	rsNoKey := base
	rsNoKey.Algorithm = "RS256"
	if _, err := auth.NewJWTMiddleware(rsNoKey); err == nil {
		t.Error("expected error for RS256 without a public key")
	}
}

func TestRS256RoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mw, err := auth.NewJWTMiddleware(auth.JWTOptions{
		Algorithm:    "RS256",
		RSAPublicKey: &key.PublicKey,
		Issuer:       testIssuer,
		Audience:     []string{testAudience},
	})
	if err != nil {
		t.Fatal(err)
	}

	var seen *auth.Principal
	h := mw(captureHandler(&seen))

	claims := jwt.MapClaims{
		"iss": testIssuer, "aud": testAudience, "sub": "user-rsa",
		"exp": time.Now().Add(time.Hour).Unix(),
	}

	good, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	if rec := request(h, good); rec.Code != http.StatusOK {
		t.Fatalf("valid RS256 token: status = %d body=%s", rec.Code, rec.Body)
	}
	if seen.Subject != "user-rsa" {
		t.Errorf("Subject = %q", seen.Subject)
	}

	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(otherKey)
	if rec := request(h, bad); rec.Code != http.StatusUnauthorized {
		t.Errorf("token from wrong key: status = %d, want 401", rec.Code)
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: mustMarshalPKIX(t, &key.PublicKey)})
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(pubPEM)
	if rec := request(h, forged); rec.Code != http.StatusUnauthorized {
		t.Errorf("algorithm-confusion token: status = %d, want 401", rec.Code)
	}
}

func mustMarshalPKIX(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestParseRSAKeysRoundTrip(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privDER, _ := x509.MarshalPKCS8PrivateKey(key)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: mustMarshalPKIX(t, &key.PublicKey)})

	if _, err := auth.ParseRSAPrivateKey(privPEM); err != nil {
		t.Errorf("ParseRSAPrivateKey: %v", err)
	}
	if _, err := auth.ParseRSAPublicKey(pubPEM); err != nil {
		t.Errorf("ParseRSAPublicKey: %v", err)
	}
	if _, err := auth.ParseRSAPublicKey([]byte("not a pem")); err == nil {
		t.Error("expected error parsing garbage public key")
	}
}

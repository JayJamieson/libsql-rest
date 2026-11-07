package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/auth"
	"github.com/JayJamieson/libsql-rest/internal/config"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(args)
	err := root.Execute()
	return strings.TrimSpace(out.String()), err
}

func accepts(t *testing.T, opts auth.JWTOptions, token string) string {
	t.Helper()
	opts.Issuer = config.DefaultIssuer
	opts.Audience = []string{config.DefaultAudience}
	mw, err := auth.NewJWTMiddleware(opts)
	if err != nil {
		t.Fatal(err)
	}
	var subject string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject = auth.FromContext(r.Context()).Subject
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("middleware rejected the token: %d %s", rec.Code, rec.Body)
	}
	return subject
}

func TestKeygenAndRS256Token(t *testing.T) {
	dir := t.TempDir()
	priv, pub := filepath.Join(dir, "priv.pem"), filepath.Join(dir, "pub.pem")

	if _, err := run(t, "keygen", "--private-out", priv, "--public-out", pub); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(priv)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key mode = %o, want 600", perm)
	}
	if _, err := run(t, "keygen", "--private-out", priv, "--public-out", pub); err == nil {
		t.Error("keygen overwrote an existing key")
	}

	token, err := run(t, "token", "--algorithm", "RS256", "--private-key", priv, "--subject", "alice")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := os.ReadFile(pub)
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.ParseRSAPublicKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	if sub := accepts(t, auth.JWTOptions{Algorithm: "RS256", RSAPublicKey: key}, token); sub != "alice" {
		t.Errorf("subject = %q, want alice", sub)
	}
}

func TestHMACToken(t *testing.T) {
	token, err := run(t, "token", "--algorithm", "hs384", "--secret", "s3cret", "--subject", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if sub := accepts(t, auth.JWTOptions{Algorithm: "HS384", HMACSecret: []byte("s3cret")}, token); sub != "bob" {
		t.Errorf("subject = %q, want bob", sub)
	}
}

func TestTokenErrors(t *testing.T) {
	cases := [][]string{
		{"token", "--algorithm", "HS256"},
		{"token", "--algorithm", "RS256"},
		{"token", "--algorithm", "ES256", "--secret", "x"},
		{"token", "--algorithm", "none", "--secret", "x"},
	}
	for _, args := range cases {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

package cmd

import (
	"cmp"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/auth"
	"github.com/JayJamieson/libsql-rest/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
)

func newTokenCmd() *cobra.Command {
	var (
		algorithm, secret, privateKey string
		subject, issuer, audience     string
		ttl                           time.Duration
		extra                         map[string]string
	)
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Mint a signed JWT for development",
		Long: `Mint a JWT that 'serve --auth' accepts.

--subject sets the sub claim. Add other claims with repeated --claim key=value
flags, such as --claim role=admin.

HS* algorithms sign with --secret. Without the flag, token reads the secret from
LIBSQL_REST_AUTH_SECRET, which keeps it out of process listings. RS* algorithms
sign with --private-key, a PEM file that 'libsql-rest keygen' writes. Give the
server the matching public key.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			alg := strings.ToUpper(algorithm)
			method := jwt.GetSigningMethod(alg)
			key, err := signingKey(method, alg, cmp.Or(secret, os.Getenv("LIBSQL_REST_AUTH_SECRET")), privateKey)
			if err != nil {
				return err
			}

			now := time.Now()
			claims := jwt.MapClaims{
				"iss": issuer,
				"aud": audience,
				"sub": subject,
				"iat": now.Unix(),
				"exp": now.Add(ttl).Unix(),
			}
			for k, v := range extra {
				claims[k] = v
			}
			signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), signed)
			return err
		},
	}

	f := cmd.Flags()
	f.StringVar(&algorithm, "algorithm", config.DefaultAlgorithm, "HS256, HS384, HS512, RS256, RS384, or RS512")
	f.StringVar(&secret, "secret", "", "HMAC secret for HS* algorithms (default $LIBSQL_REST_AUTH_SECRET)")
	f.StringVar(&privateKey, "private-key", "", "PEM file with the RSA private key for RS* algorithms")
	f.StringVar(&subject, "subject", "", "sub claim, the user ID")
	f.StringVar(&issuer, "issuer", config.DefaultIssuer, "iss claim")
	f.StringVar(&audience, "audience", config.DefaultAudience, "aud claim")
	f.DurationVar(&ttl, "ttl", 24*time.Hour, "time until the token expires")
	f.StringToStringVar(&extra, "claim", nil, "extra claim as key=value; repeat for more")
	return cmd
}

func signingKey(method jwt.SigningMethod, alg, secret, privateKeyPath string) (any, error) {
	switch method.(type) {
	case *jwt.SigningMethodHMAC:
		if strings.TrimSpace(secret) == "" {
			return nil, fmt.Errorf("--secret or LIBSQL_REST_AUTH_SECRET is required for %s", alg)
		}
		return []byte(secret), nil
	case *jwt.SigningMethodRSA:
		if privateKeyPath == "" {
			return nil, fmt.Errorf("--private-key is required for %s", alg)
		}
		pemBytes, err := os.ReadFile(privateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading private key: %w", err)
		}
		return auth.ParseRSAPrivateKey(pemBytes)
	default:
		return nil, fmt.Errorf("unsupported algorithm %q (want HS256, HS384, HS512, RS256, RS384, or RS512)", alg)
	}
}

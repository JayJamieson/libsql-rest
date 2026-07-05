package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/auth"
	"github.com/JayJamieson/libsql-rest/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
)

// tokenCmd mints a signed JWT for local development and testing. It is a
// convenience for self-hosted setups; production tokens would come from your
// identity provider. HS256 signs with a shared secret; RS256 signs with a
// private key whose public half the server uses to verify.
var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "mint a signed JWT for local development",
	Long: `Mint a signed JWT compatible with 'serve --auth'.

The --subject flag sets the 'sub' (user id) claim that row-level security keys
off. Extra claims can be added with repeated --claim key=value flags, which is
handy for role-based rules, e.g. --claim role=admin.

HS256 (default) signs with --secret. RS256 signs with --private-key (a PEM file
produced by 'libsql-rest keygen'); point the server at the matching public key.`,
	RunE: runToken,
}

func runToken(cmd *cobra.Command, args []string) error {
	algorithm := strings.ToUpper(mustString(cmd, "algorithm"))
	subject := mustString(cmd, "subject")
	issuer := mustString(cmd, "issuer")
	audience := mustString(cmd, "audience")
	ttl, _ := cmd.Flags().GetDuration("ttl")
	extra, _ := cmd.Flags().GetStringToString("claim")

	now := time.Now()
	claims := jwt.MapClaims{
		"iss": issuer,
		"aud": audience,
		"sub": subject, // the user id RLS reads via Principal.UserID()
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}

	signed, err := signToken(cmd, algorithm, claims)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), signed)
	return nil
}

// signToken signs the claims with the algorithm-appropriate key material.
func signToken(cmd *cobra.Command, algorithm string, claims jwt.MapClaims) (string, error) {
	switch algorithm {
	case "HS256":
		secret := mustString(cmd, "secret")
		if strings.TrimSpace(secret) == "" {
			return "", fmt.Errorf("--secret is required for HS256")
		}
		return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	case "RS256":
		path := mustString(cmd, "private-key")
		if strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("--private-key is required for RS256")
		}
		pemBytes, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading private key: %w", err)
		}
		key, err := auth.ParseRSAPrivateKey(pemBytes)
		if err != nil {
			return "", err
		}
		return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	default:
		return "", fmt.Errorf("unsupported algorithm %q (use HS256 or RS256)", algorithm)
	}
}

func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func init() {
	rootCmd.AddCommand(tokenCmd)
	tokenCmd.Flags().String("algorithm", "HS256", "signing algorithm: HS256 or RS256")
	tokenCmd.Flags().String("secret", "", "HMAC secret to sign HS256 tokens")
	tokenCmd.Flags().String("private-key", "", "PEM private key path to sign RS256 tokens")
	tokenCmd.Flags().String("subject", "", "subject (user id) claim")
	tokenCmd.Flags().String("issuer", config.DefaultIssuer, "issuer claim")
	tokenCmd.Flags().String("audience", config.DefaultAudience, "audience claim")
	tokenCmd.Flags().Duration("ttl", 24*time.Hour, "token lifetime")
	tokenCmd.Flags().StringToString("claim", nil, "additional claim(s) as key=value")
}

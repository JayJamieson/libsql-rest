package cmd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// keygenCmd generates an RSA key pair for local RS256 development: the private
// key mints tokens (`token --algorithm RS256 --private-key ...`) and the public
// key verifies them (`serve --auth-algorithm RS256 --auth-public-key ...`).
var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "generate an RSA key pair for RS256 JWTs",
	Long: `Generate an RSA key pair for local RS256 development.

The private key signs tokens; the server only needs the public key to verify
them. Do not use development keys in production.`,
	RunE: runKeygen,
}

func runKeygen(cmd *cobra.Command, args []string) error {
	bits, _ := cmd.Flags().GetInt("bits")
	privPath := mustString(cmd, "private-out")
	pubPath := mustString(cmd, "public-out")

	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: mustMarshalPKCS8(key),
	})
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return fmt.Errorf("marshaling public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})

	// Private key is secret material: write it 0600.
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return fmt.Errorf("writing private key: %w", err)
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		return fmt.Errorf("writing public key: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "wrote private key to %s\nwrote public key to %s\n", privPath, pubPath)
	return nil
}

func mustMarshalPKCS8(key *rsa.PrivateKey) []byte {
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		// MarshalPKCS8PrivateKey only errors for unsupported key types; RSA is
		// always supported, so this is unreachable.
		panic(err)
	}
	return b
}

func init() {
	rootCmd.AddCommand(keygenCmd)
	keygenCmd.Flags().Int("bits", 2048, "RSA key size in bits")
	keygenCmd.Flags().String("private-out", "jwt-private.pem", "path to write the private key")
	keygenCmd.Flags().String("public-out", "jwt-public.pem", "path to write the public key")
}

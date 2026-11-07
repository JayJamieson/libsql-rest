package cmd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newKeygenCmd() *cobra.Command {
	var (
		bits            int
		privOut, pubOut string
	)
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an RSA key pair for RS256 tokens",
		Long: `Generate an RSA key pair for development with RS256 tokens.

'token --private-key' signs with the private key, and 'serve --auth-public-key'
verifies with the public key. keygen does not overwrite existing files.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := rsa.GenerateKey(rand.Reader, bits)
			if err != nil {
				return fmt.Errorf("generating key: %w", err)
			}
			privDER, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				return fmt.Errorf("encoding private key: %w", err)
			}
			pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
			if err != nil {
				return fmt.Errorf("encoding public key: %w", err)
			}

			if err := writeNewFile(privOut, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600); err != nil {
				return err
			}
			if err := writeNewFile(pubOut, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644); err != nil {
				return errors.Join(err, os.Remove(privOut))
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote private key to %s\nwrote public key to %s\n", privOut, pubOut)
			return err
		},
	}

	f := cmd.Flags()
	f.IntVar(&bits, "bits", 2048, "RSA key size in bits")
	f.StringVar(&privOut, "private-out", "jwt-private.pem", "path for the private key")
	f.StringVar(&pubOut, "public-out", "jwt-public.pem", "path for the public key")
	return cmd
}

func writeNewFile(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

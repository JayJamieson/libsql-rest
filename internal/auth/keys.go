package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// ParseRSAPublicKey parses an RSA public key from PEM bytes. It accepts both
// PKIX ("BEGIN PUBLIC KEY") and PKCS#1 ("BEGIN RSA PUBLIC KEY") encodings, which
// covers keys produced by openssl and most identity providers.
func ParseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in public key")
	}
	switch block.Type {
	case "PUBLIC KEY":
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing PKIX public key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("public key is %T, want RSA", key)
		}
		return rsaKey, nil
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported public key PEM type %q", block.Type)
	}
}

// ParseRSAPrivateKey parses an RSA private key from PEM bytes. It accepts both
// PKCS#1 ("BEGIN RSA PRIVATE KEY") and PKCS#8 ("BEGIN PRIVATE KEY") encodings.
// It is used only by the local token-minting command, not by the server.
func ParseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parsing PKCS8 private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is %T, want RSA", key)
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported private key PEM type %q", block.Type)
	}
}

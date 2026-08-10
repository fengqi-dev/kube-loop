package token

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"strings"
)

const maxSigningKeyBytes = 64 << 10

func LoadSigningKey(path string) (ed25519.PrivateKey, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("token signing key file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open token signing key file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSigningKeyBytes+1))
	if err != nil || len(data) > maxSigningKeyBytes {
		return nil, errors.New("read token signing key file")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("token signing key must be one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse token signing key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("token signing key must use Ed25519")
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

package oauthserver

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"strings"
)

const maxKeyBytes = 64 << 10

func LoadSigningKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := readSecretFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(raw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("oidc signing key must be one PEM private key")
	}
	var parsed any
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, errors.New(
			"oidc signing key must be PKCS#8 or SEC1 ECDSA PEM",
		)
	}
	if err != nil {
		return nil, errors.New("parse OIDC signing key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve.Params().Name != "P-256" {
		return nil, errors.New("oidc signing key must use ECDSA P-256")
	}
	return key, nil
}

func LoadHMACSecret(path string) ([]byte, error) {
	raw, err := readSecretFile(path)
	if err != nil {
		return nil, err
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) != 32 {
		return nil, errors.New(
			"fosite HMAC secret must contain exactly 32 bytes",
		)
	}
	return append([]byte(nil), raw...), nil
}

func readSecretFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("authentication secret file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open authentication secret file")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxKeyBytes+1))
	if err != nil || len(raw) > maxKeyBytes {
		return nil, errors.New("read authentication secret file")
	}
	return raw, nil
}

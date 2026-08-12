package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSigningKeyAcceptsOnlyEd25519PKCS8(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o400); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSigningKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !privateKey.Equal(loaded) {
		t.Fatal("loaded signing key differs")
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaEncoded, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaPath := filepath.Join(t.TempDir(), "rsa-key.pem")
	if err := os.WriteFile(rsaPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaEncoded}), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSigningKey(rsaPath); err == nil {
		t.Fatal("RSA key was accepted")
	}
}

package relayticket

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
)

const maximumKeyFileBytes = 256 << 10

type publicKeysFile struct {
	Keys []publicKeyEntry `json:"keys"`
}

type publicKeyEntry struct {
	KeyID     string `json:"kid"`
	PublicKey string `json:"publicKeyPem"`
}

func LoadSigningKey(path string) (ed25519.PrivateKey, error) {
	data, err := readKeyFile(path)
	if err != nil {
		return nil, errors.New("read relay ticket signing key file")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("relay ticket signing key must be one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("parse relay ticket signing key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("relay ticket signing key must use Ed25519")
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

func LoadVerificationKeys(path string) (map[string]ed25519.PublicKey, error) {
	data, err := readKeyFile(path)
	if err != nil {
		return nil, errors.New("read relay ticket verification keys file")
	}
	var document publicKeysFile
	if err := decodeStrict(data, &document); err != nil || len(document.Keys) == 0 || len(document.Keys) > 8 {
		return nil, errors.New("parse relay ticket verification keys file")
	}
	keys := make(map[string]ed25519.PublicKey, len(document.Keys))
	for _, entry := range document.Keys {
		if !validIdentifier(entry.KeyID, 128) {
			return nil, errors.New("relay ticket verification key ID is invalid")
		}
		if _, exists := keys[entry.KeyID]; exists {
			return nil, errors.New("relay ticket verification key ID is duplicated")
		}
		block, rest := pem.Decode([]byte(entry.PublicKey))
		if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
			return nil, errors.New("relay ticket verification key must be one PKIX PEM public key")
		}
		parsed, parseErr := x509.ParsePKIXPublicKey(block.Bytes)
		if parseErr != nil {
			return nil, errors.New("parse relay ticket verification key")
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("relay ticket verification key must use Ed25519")
		}
		keys[entry.KeyID] = append(ed25519.PublicKey(nil), key...)
	}
	return keys, nil
}

func readKeyFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("key file path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumKeyFileBytes+1))
	if err != nil || len(data) == 0 || len(data) > maximumKeyFileBytes {
		return nil, errors.New("key file is invalid")
	}
	return data, nil
}

// MarshalVerificationKeys is intended for provisioning tools and tests. The
// private key is never included in the resulting JSON document.
func MarshalVerificationKeys(keys map[string]ed25519.PublicKey) ([]byte, error) {
	if len(keys) == 0 || len(keys) > 8 {
		return nil, errors.New("relay ticket verification keys are invalid")
	}
	document := publicKeysFile{Keys: make([]publicKeyEntry, 0, len(keys))}
	keyIDs := make([]string, 0, len(keys))
	for keyID := range keys {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	for _, keyID := range keyIDs {
		key := keys[keyID]
		if !validIdentifier(keyID, 128) || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("relay ticket verification key is invalid")
		}
		encoded, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return nil, errors.New("encode relay ticket verification key")
		}
		document.Keys = append(document.Keys, publicKeyEntry{
			KeyID: keyID, PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})),
		})
	}
	return json.Marshal(document)
}

package relaycontrol

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"sort"
	"time"
)

func NewVerificationKeySet(
	generation uint64,
	keys map[string]ed25519.PublicKey,
	notBefore, notAfter time.Time,
) (VerificationKeySet, error) {
	keyIDs := make([]string, 0, len(keys))
	for keyID := range keys {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	result := VerificationKeySet{Generation: generation, Keys: make([]VerificationKey, 0, len(keys))}
	for _, keyID := range keyIDs {
		key := keys[keyID]
		if len(key) != ed25519.PublicKeySize {
			return VerificationKeySet{}, errors.New("RelayTicket verification key is invalid")
		}
		encoded, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return VerificationKeySet{}, errors.New("encode RelayTicket verification key")
		}
		result.Keys = append(result.Keys, VerificationKey{
			ID: keyID, Algorithm: "EdDSA",
			PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})),
			NotBefore: notBefore.UTC(), NotAfter: notAfter.UTC(),
		})
	}
	if err := result.Validate(notBefore.UTC()); err != nil {
		return VerificationKeySet{}, err
	}
	return result, nil
}

type ParsedVerificationKey struct {
	PublicKey ed25519.PublicKey
	NotBefore time.Time
	NotAfter  time.Time
}

func (keys VerificationKeySet) Parse(now time.Time) (map[string]ParsedVerificationKey, error) {
	if err := keys.Validate(now.UTC()); err != nil {
		return nil, err
	}
	result := make(map[string]ParsedVerificationKey, len(keys.Keys))
	for _, document := range keys.Keys {
		block, _ := pem.Decode([]byte(document.PublicKey))
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		key, ok := parsed.(ed25519.PublicKey)
		if err != nil || !ok {
			return nil, errors.New("parse RelayTicket verification key")
		}
		result[document.ID] = ParsedVerificationKey{
			PublicKey: append(ed25519.PublicKey(nil), key...),
			NotBefore: document.NotBefore, NotAfter: document.NotAfter,
		}
	}
	return result, nil
}

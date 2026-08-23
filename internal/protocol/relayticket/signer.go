package relayticket

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

type Signer struct {
	keyID string
	key   ed25519.PrivateKey
}

func NewSigner(keyID string, key ed25519.PrivateKey) (*Signer, error) {
	keyID = strings.TrimSpace(keyID)
	if !validIdentifier(keyID, 128) || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("relay ticket signer configuration is invalid")
	}
	return &Signer{keyID: keyID, key: append(ed25519.PrivateKey(nil), key...)}, nil
}

func (signer *Signer) Sign(claims Claims) (string, error) {
	if signer == nil || len(signer.key) != ed25519.PrivateKeySize {
		return "", errors.New("relay ticket signer is unavailable")
	}
	if err := validateClaims(claims); err != nil {
		return "", err
	}
	headerJSON, err := json.Marshal(header{Algorithm: Algorithm, Type: Type, KeyID: signer.keyID})
	if err != nil {
		return "", errors.New("encode relay ticket header")
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", errors.New("encode relay ticket claims")
	}
	unsigned := base64.RawURLEncoding.EncodeToString(
		headerJSON,
	) + "." + base64.RawURLEncoding.EncodeToString(
		claimsJSON,
	)
	signature := ed25519.Sign(signer.key, []byte(unsigned))
	ticket := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(ticket) > MaximumTicketBytes {
		return "", errors.New("relay ticket exceeds size limit")
	}
	return ticket, nil
}

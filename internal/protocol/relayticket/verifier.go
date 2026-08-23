package relayticket

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

type VerifierConfig struct {
	Keys              map[string]ed25519.PublicKey
	KeyValidity       map[string]KeyValidity
	Issuer            string
	Audience          string
	RequiredOperation string
	ClockSkew         time.Duration
	Now               func() time.Time
}

type KeyValidity struct {
	NotBefore time.Time
	NotAfter  time.Time
}

type Verifier struct {
	keys              map[string]ed25519.PublicKey
	keyValidity       map[string]KeyValidity
	issuer            string
	audience          string
	requiredOperation string
	clockSkew         time.Duration
	now               func() time.Time
}

func NewVerifier(config VerifierConfig) (*Verifier, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	config.Audience = strings.TrimSpace(config.Audience)
	config.RequiredOperation = strings.TrimSpace(config.RequiredOperation)
	if !validText(config.Issuer, 512) || !validIdentifier(config.Audience, 128) ||
		!validIdentifier(config.RequiredOperation, 64) || len(config.Keys) == 0 || len(config.Keys) > 8 {
		return nil, errors.New("relay ticket verifier configuration is invalid")
	}
	if config.ClockSkew == 0 {
		config.ClockSkew = DefaultClockSkew
	}
	if config.ClockSkew < 0 || config.ClockSkew > time.Minute {
		return nil, errors.New("relay ticket verifier clock skew is invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	keys := make(map[string]ed25519.PublicKey, len(config.Keys))
	keyValidity := make(map[string]KeyValidity, len(config.KeyValidity))
	for keyID, key := range config.Keys {
		if !validIdentifier(keyID, 128) || len(key) != ed25519.PublicKeySize {
			return nil, errors.New("relay ticket verification key is invalid")
		}
		keys[keyID] = append(ed25519.PublicKey(nil), key...)
		if validity, exists := config.KeyValidity[keyID]; exists {
			validity.NotBefore = validity.NotBefore.UTC()
			validity.NotAfter = validity.NotAfter.UTC()
			if validity.NotBefore.IsZero() || validity.NotAfter.IsZero() ||
				!validity.NotAfter.After(validity.NotBefore) {
				return nil, errors.New("relay ticket verification key validity is invalid")
			}
			keyValidity[keyID] = validity
		}
	}
	for keyID := range config.KeyValidity {
		if _, exists := keys[keyID]; !exists {
			return nil, errors.New("relay ticket verification key validity has no matching key")
		}
	}
	return &Verifier{
		keys: keys, keyValidity: keyValidity, issuer: config.Issuer, audience: config.Audience,
		requiredOperation: config.RequiredOperation, clockSkew: config.ClockSkew, now: config.Now,
	}, nil
}

func (verifier *Verifier) Verify(ticket string) (Claims, error) {
	if verifier == nil || len(ticket) == 0 || len(ticket) > MaximumTicketBytes || strings.TrimSpace(ticket) != ticket {
		return Claims{}, ErrInvalid
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Claims{}, ErrInvalid
	}
	headerJSON, err := decodePart(parts[0], 1024)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var ticketHeader header
	if err := decodeStrict(headerJSON, &ticketHeader); err != nil || ticketHeader.Algorithm != Algorithm ||
		ticketHeader.Type != Type || !validIdentifier(ticketHeader.KeyID, 128) {
		return Claims{}, ErrInvalid
	}
	key, exists := verifier.keys[ticketHeader.KeyID]
	if !exists {
		return Claims{}, ErrInvalid
	}
	nowTime := verifier.now().UTC()
	if validity, bounded := verifier.keyValidity[ticketHeader.KeyID]; bounded {
		beforeValidity := nowTime.Add(verifier.clockSkew).Before(validity.NotBefore)
		afterValidity := !nowTime.Add(-verifier.clockSkew).Before(validity.NotAfter)
		if beforeValidity || afterValidity {
			return Claims{}, ErrInvalid
		}
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, ErrInvalid
	}
	claimsJSON, err := decodePart(parts[1], MaximumTicketBytes)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := decodeStrict(claimsJSON, &claims); err != nil || validateClaims(claims) != nil {
		return Claims{}, ErrInvalid
	}
	now := nowTime.Unix()
	skew := int64(verifier.clockSkew / time.Second)
	if claims.Issuer != verifier.issuer || claims.Audience != verifier.audience ||
		claims.NotBefore > now+skew || claims.IssuedAt > now+skew || claims.ExpiresAt <= now-skew ||
		!contains(claims.Operations, verifier.requiredOperation) {
		return Claims{}, ErrInvalid
	}
	return claims, nil
}

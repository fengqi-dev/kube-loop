package relayticket

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	Version            = 2
	Type               = "KubeLoop-RelayTicket"
	Algorithm          = "EdDSA"
	DefaultLifetime    = time.Minute
	MaximumLifetime    = 2 * time.Minute
	DefaultClockSkew   = 15 * time.Second
	MaximumTicketBytes = 8 << 10
)

var ErrInvalid = errors.New("invalid relay ticket")

type Claims struct {
	Version           int      `json:"ver"`
	Issuer            string   `json:"iss"`
	Audience          string   `json:"aud"`
	IdentityID        string   `json:"sub"`
	Groups            []string `json:"groups,omitempty"`
	DeviceID          string   `json:"device_id"`
	SessionID         string   `json:"session_id"`
	SessionGeneration uint64   `json:"session_generation"`
	Namespace         string   `json:"namespace"`
	Operations        []string `json:"operations"`
	NetworkSpecHash   string   `json:"network_spec_hash"`
	TicketID          string   `json:"jti"`
	IssuedAt          int64    `json:"iat"`
	NotBefore         int64    `json:"nbf"`
	ExpiresAt         int64    `json:"exp"`
}

type header struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

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

func validateClaims(claims Claims) error {
	if claims.Version != Version || !validText(claims.Issuer, 512) || !validIdentifier(claims.Audience, 128) ||
		!validIdentifier(claims.IdentityID, 256) || !validIdentifier(claims.DeviceID, 256) ||
		!validIdentifier(claims.SessionID, 128) || !validIdentifier(claims.Namespace, 253) ||
		claims.SessionGeneration == 0 || !validIdentifier(claims.TicketID, 128) ||
		len(claims.Groups) > 128 || len(claims.Operations) == 0 || len(claims.Operations) > 16 {
		return errors.New("relay ticket claims are invalid")
	}
	groups := make(map[string]struct{}, len(claims.Groups))
	for _, group := range claims.Groups {
		if !validText(group, 256) {
			return errors.New("relay ticket group is invalid")
		}
		if _, exists := groups[group]; exists {
			return errors.New("relay ticket group is duplicated")
		}
		groups[group] = struct{}{}
	}
	seen := make(map[string]struct{}, len(claims.Operations))
	for _, operation := range claims.Operations {
		if !validIdentifier(operation, 64) {
			return errors.New("relay ticket operation is invalid")
		}
		if _, exists := seen[operation]; exists {
			return errors.New("relay ticket operation is duplicated")
		}
		seen[operation] = struct{}{}
	}
	if !validLowerHex(claims.NetworkSpecHash, 64) {
		return errors.New("relay ticket network spec hash is invalid")
	}
	if claims.IssuedAt <= 0 || claims.NotBefore <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.NotBefore < claims.IssuedAt || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > MaximumLifetime {
		return errors.New("relay ticket lifetime is invalid")
	}
	return nil
}

func decodePart(value string, maximum int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > maximum {
		return nil, ErrInvalid
	}
	return decoded, nil
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !containsControl(value)
}

func validIdentifier(value string, maximum int) bool {
	if !validText(value, maximum) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._:@/", character) {
			continue
		}
		return false
	}
	return true
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}

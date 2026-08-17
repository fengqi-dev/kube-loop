package relayticket

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayTicketRoundTripAndBindings(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("key-2026-08", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now)
	ticket, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Keys:   map[string]ed25519.PublicKey{"key-2026-08": publicKey},
		Issuer: claims.Issuer, Audience: claims.Audience, RequiredOperation: "tunnel",
		Now: func() time.Time { return now.Add(30 * time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if verified.SessionID != claims.SessionID || verified.DeviceID != claims.DeviceID || verified.Namespace != claims.Namespace ||
		len(verified.Groups) != 1 || verified.Groups[0] != "developers" {
		t.Fatalf("verified claims = %#v", verified)
	}

	for name, mutate := range map[string]func(VerifierConfig) VerifierConfig{
		"issuer":    func(config VerifierConfig) VerifierConfig { config.Issuer = "https://other.example"; return config },
		"audience":  func(config VerifierConfig) VerifierConfig { config.Audience = "relay-b"; return config },
		"operation": func(config VerifierConfig) VerifierConfig { config.RequiredOperation = "exec"; return config },
		"unknown-key": func(config VerifierConfig) VerifierConfig {
			config.Keys = map[string]ed25519.PublicKey{"other": publicKey}
			return config
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := mutate(VerifierConfig{
				Keys:   map[string]ed25519.PublicKey{"key-2026-08": publicKey},
				Issuer: claims.Issuer, Audience: claims.Audience, RequiredOperation: "tunnel",
				Now: func() time.Time { return now.Add(30 * time.Second) },
			})
			boundVerifier, verifierErr := NewVerifier(config)
			if verifierErr != nil {
				t.Fatal(verifierErr)
			}
			if _, verifyErr := boundVerifier.Verify(ticket); !errors.Is(verifyErr, ErrInvalid) {
				t.Fatalf("Verify error = %v", verifyErr)
			}
		})
	}
}

func TestRelayTicketRejectsTamperingAndInvalidTimes(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifierAt := func(instant time.Time) *Verifier {
		verifier, verifierErr := NewVerifier(VerifierConfig{
			Keys: map[string]ed25519.PublicKey{"primary": publicKey}, Issuer: "https://control-plane.example",
			Audience: "relay-a", RequiredOperation: "tunnel", ClockSkew: time.Second,
			Now: func() time.Time { return instant },
		})
		if verifierErr != nil {
			t.Fatal(verifierErr)
		}
		return verifier
	}
	claims := validClaims(now)
	ticket, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(ticket, ".")
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON[len(claimsJSON)/2] ^= 1
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(claimsJSON) + "." + parts[2]
	if _, err := verifierAt(now.Add(30 * time.Second)).Verify(tampered); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered Verify error = %v", err)
	}
	if _, err := verifierAt(now.Add(2 * time.Minute)).Verify(ticket); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired Verify error = %v", err)
	}
	if _, err := verifierAt(now.Add(-time.Minute)).Verify(ticket); !errors.Is(err, ErrInvalid) {
		t.Fatalf("early Verify error = %v", err)
	}

	tooLong := claims
	tooLong.ExpiresAt = now.Add(MaximumLifetime + time.Second).Unix()
	if _, err := signer.Sign(tooLong); err == nil {
		t.Fatal("overlong ticket was signed")
	}
	duplicateScope := claims
	duplicateScope.Operations = []string{"tunnel", "tunnel"}
	if _, err := signer.Sign(duplicateScope); err == nil {
		t.Fatal("duplicate operation ticket was signed")
	}
	withoutNetworkBinding := claims
	withoutNetworkBinding.NetworkSpecHash = ""
	if _, err := signer.Sign(withoutNetworkBinding); err == nil {
		t.Fatal("ticket without a NetworkSpec binding was signed")
	}
}

func TestRelayTicketStrictEncodingAndSizeLimit(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Keys: map[string]ed25519.PublicKey{"primary": publicKey}, Issuer: "https://control-plane.example",
		Audience: "relay-a", RequiredOperation: "tunnel", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now)
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	headerJSON := []byte(`{"alg":"EdDSA","typ":"KubeLoop-RelayTicket","kid":"primary","extra":true}`)
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := ed25519.Sign(privateKey, []byte(unsigned))
	unknownHeader := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	if _, err := verifier.Verify(unknownHeader); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown-header Verify error = %v", err)
	}
	if _, err := verifier.Verify(strings.Repeat("x", MaximumTicketBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized Verify error = %v", err)
	}
}

func TestRelayTicketKeyFilesAndRotation(t *testing.T) {
	directory := t.TempDir()
	keys := make(map[string]ed25519.PublicKey)
	var signingKey ed25519.PrivateKey
	for _, keyID := range []string{"old", "new"} {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keys[keyID] = publicKey
		if keyID == "new" {
			signingKey = privateKey
		}
	}
	encodedPrivate, err := x509.MarshalPKCS8PrivateKey(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(directory, "signing-key.pem")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedPrivate}), 0o400); err != nil {
		t.Fatal(err)
	}
	loadedPrivate, err := LoadSigningKey(privatePath)
	if err != nil || !loadedPrivate.Equal(signingKey) {
		t.Fatalf("LoadSigningKey error = %v", err)
	}
	publicJSON, err := MarshalVerificationKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(directory, "verification-keys.json")
	if err := os.WriteFile(publicPath, publicJSON, 0o400); err != nil {
		t.Fatal(err)
	}
	loadedKeys, err := LoadVerificationKeys(publicPath)
	if err != nil || len(loadedKeys) != 2 || !loadedKeys["new"].Equal(keys["new"]) || !loadedKeys["old"].Equal(keys["old"]) {
		t.Fatalf("LoadVerificationKeys = %#v, error = %v", loadedKeys, err)
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaEncoded, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	rsaPath := filepath.Join(directory, "rsa.pem")
	if err := os.WriteFile(rsaPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaEncoded}), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSigningKey(rsaPath); err == nil {
		t.Fatal("RSA relay signing key was accepted")
	}
}

func TestVerifierEnforcesPublishedKeyValidityWindow(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now)
	ticket, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		validity KeyValidity
	}{
		{name: "not yet active", validity: KeyValidity{NotBefore: now.Add(time.Minute), NotAfter: now.Add(time.Hour)}},
		{name: "expired", validity: KeyValidity{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(-time.Minute)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := NewVerifier(VerifierConfig{
				Keys:        map[string]ed25519.PublicKey{"primary": publicKey},
				KeyValidity: map[string]KeyValidity{"primary": test.validity},
				Issuer:      claims.Issuer, Audience: claims.Audience, RequiredOperation: "tunnel",
				ClockSkew: time.Nanosecond, Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.Verify(ticket); err == nil {
				t.Fatal("ticket signed by an out-of-window key was accepted")
			}
		})
	}
}

func validClaims(now time.Time) Claims {
	return Claims{
		Version: Version, Issuer: "https://control-plane.example", Audience: "relay-a",
		IdentityID: "11111111-1111-4111-8111-111111111111",
		Groups:     []string{"developers"},
		DeviceID:   "22222222-2222-4222-8222-222222222222",
		SessionID:  "33333333-3333-4333-8333-333333333333", SessionGeneration: 7, Namespace: "development",
		Operations: []string{"tunnel"}, NetworkSpecHash: strings.Repeat("a", 64),
		TicketID: "44444444-4444-4444-8444-444444444444",
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	}
}

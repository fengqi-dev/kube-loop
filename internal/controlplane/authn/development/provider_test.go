package development

import (
	"bytes"
	"context"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
)

func TestStaticTokenUsesConstantCredentialBoundaryAndClearsInputs(t *testing.T) {
	configured := []byte("0123456789abcdef0123456789abcdef")
	provider, err := NewStaticToken("local", "Development Token", configured, IdentityConfig{
		Subject: "developer-1", DisplayName: "Local Developer", Groups: []string{"writers", "readers", "writers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configured, make([]byte, len(configured))) {
		t.Fatal("configured static token was not cleared after hashing")
	}
	presented := []byte("0123456789abcdef0123456789abcdef")
	identity, err := provider.AuthenticateToken(context.Background(), authn.TokenCredentials{Token: presented})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(presented, make([]byte, len(presented))) {
		t.Fatal("presented static token was not cleared")
	}
	if identity.ProviderID != "local" || identity.DevelopmentSubject != "developer-1" ||
		len(identity.Groups) != 2 || identity.Groups[0] != "readers" {
		t.Fatalf("identity = %#v", identity)
	}
	identity.Groups[0] = "mutated"
	again := []byte("0123456789abcdef0123456789abcdef")
	second, err := provider.AuthenticateToken(context.Background(), authn.TokenCredentials{Token: again})
	if err != nil || second.Groups[0] != "readers" {
		t.Fatalf("provider identity was mutable: %#v, %v", second, err)
	}
	wrong := []byte("fedcba9876543210fedcba9876543210")
	if _, err := provider.AuthenticateToken(context.Background(), authn.TokenCredentials{Token: wrong}); err == nil {
		t.Fatal("wrong static token was accepted")
	}
	if !bytes.Equal(wrong, make([]byte, len(wrong))) {
		t.Fatal("rejected static token was not cleared")
	}
}

func TestDevelopmentProvidersRequireSafeIdentityInputs(t *testing.T) {
	if _, err := NewStaticToken("local", "", []byte("too-short"), IdentityConfig{}); err == nil {
		t.Fatal("short static token was accepted")
	}
	provider, err := NewAnonymous("anonymous", "Anonymous (unsafe)", IdentityConfig{Groups: []string{"developers"}})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := provider.AuthenticateAnonymous(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	externalID, err := identity.ExternalID()
	if err != nil || externalID != "anonymous" || identity.ProviderID != "anonymous" {
		t.Fatalf("anonymous identity = %#v, externalID=%q, error=%v", identity, externalID, err)
	}
	if _, err := NewAnonymous("anonymous", "", IdentityConfig{Groups: []string{""}}); err == nil {
		t.Fatal("empty development group was accepted")
	}
}

package development

import (
	"context"
	"testing"
)

func TestDevelopmentProvidersRequireSafeIdentityInputs(t *testing.T) {
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

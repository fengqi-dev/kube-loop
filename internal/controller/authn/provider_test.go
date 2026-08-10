package authn

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeProvider struct {
	descriptor Descriptor
	checkErr   error
}

func (provider fakeProvider) Descriptor() Descriptor      { return provider.descriptor }
func (provider fakeProvider) Check(context.Context) error { return provider.checkErr }

func TestIdentityExternalIDUsesImmutableOIDCIdentity(t *testing.T) {
	identity := Identity{Issuer: "https://login.example.com/", Subject: "248289761001"}
	externalID, err := identity.ExternalID()
	if err != nil {
		t.Fatal(err)
	}
	if externalID != "https://login.example.com\x00248289761001" {
		t.Fatalf("external ID = %q", externalID)
	}
	identity.Email = "changed@example.com"
	again, err := identity.ExternalID()
	if err != nil || again != externalID {
		t.Fatalf("mutable profile changed external ID: %q, %v", again, err)
	}
}

func TestIdentityExternalIDUsesImmutableADObjectID(t *testing.T) {
	identity := Identity{DirectoryID: "corp", ObjectID: "S-1-5-21-42", Email: "user@example.com"}
	externalID, err := identity.ExternalID()
	if err != nil {
		t.Fatal(err)
	}
	if externalID != "corp\x00S-1-5-21-42" {
		t.Fatalf("external ID = %q", externalID)
	}
}

func TestRegistryValidatesAndDoesNotExposeProviderConfiguration(t *testing.T) {
	oidc := fakeProvider{descriptor: Descriptor{
		ID: "corporate", Type: ProviderOIDC, DisplayName: "Corporate SSO", Interaction: InteractionBrowser,
	}}
	ad := fakeProvider{descriptor: Descriptor{
		ID: "legacy-ad", Type: ProviderAD, DisplayName: "Legacy AD", Interaction: InteractionPassword,
	}}
	registry, err := NewRegistry(oidc, ad)
	if err != nil {
		t.Fatal(err)
	}
	want := []Descriptor{oidc.descriptor, ad.descriptor}
	if got := registry.Descriptors(); !reflect.DeepEqual(got, want) {
		t.Fatalf("descriptors = %#v", got)
	}
	copy := registry.Descriptors()
	copy[0].ID = "mutated"
	if registry.Descriptors()[0].ID != "corporate" {
		t.Fatal("registry descriptors escaped mutable storage")
	}
}

func TestRegistryRejectsDuplicateAndInvalidProviders(t *testing.T) {
	valid := fakeProvider{descriptor: Descriptor{ID: "corp", Type: ProviderOIDC, Interaction: InteractionBrowser}}
	if _, err := NewRegistry(valid, valid); err == nil {
		t.Fatal("expected duplicate provider error")
	}
	invalid := fakeProvider{descriptor: Descriptor{ID: "corp", Type: ProviderOIDC, Interaction: InteractionPassword}}
	if _, err := NewRegistry(invalid); err == nil {
		t.Fatal("expected interaction mismatch")
	}
}

func TestRegistryCheckFailsClosed(t *testing.T) {
	want := errors.New("upstream unavailable")
	registry, err := NewRegistry(fakeProvider{
		descriptor: Descriptor{ID: "corp", Type: ProviderOIDC, Interaction: InteractionBrowser},
		checkErr:   want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Check(context.Background()); !errors.Is(err, want) {
		t.Fatalf("check error = %v", err)
	}
}

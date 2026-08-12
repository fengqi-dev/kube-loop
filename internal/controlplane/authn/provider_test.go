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

func TestRegistryValidatesAndDoesNotExposeProviderConfiguration(t *testing.T) {
	oidc := fakeProvider{descriptor: Descriptor{
		ID: "corporate", Type: ProviderOIDC, DisplayName: "Corporate SSO", Interaction: InteractionBrowser,
	}}
	anonymous := fakeProvider{descriptor: Descriptor{
		ID: "guest", Type: ProviderAnonymous, DisplayName: "Anonymous", Interaction: InteractionNone,
	}}
	registry, err := NewRegistry(oidc, anonymous)
	if err != nil {
		t.Fatal(err)
	}
	want := []Descriptor{oidc.descriptor, anonymous.descriptor}
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
	invalid := fakeProvider{descriptor: Descriptor{ID: "corp", Type: ProviderOIDC, Interaction: InteractionNone}}
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

func TestRegistryReplaceAtomicallyUpdatesAllReaders(t *testing.T) {
	first := fakeProvider{descriptor: Descriptor{ID: "first", Type: ProviderOIDC, Interaction: InteractionBrowser}}
	second := fakeProvider{descriptor: Descriptor{ID: "second", Type: ProviderAnonymous, Interaction: InteractionNone}}
	registry, err := NewRegistry(first)
	if err != nil {
		t.Fatal(err)
	}
	next, err := NewRegistry(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Replace(next); err != nil {
		t.Fatal(err)
	}
	if _, exists := registry.Provider("first"); exists {
		t.Fatal("replaced provider remained visible")
	}
	provider, exists := registry.Provider("second")
	if !exists || provider.Descriptor().Type != ProviderAnonymous || len(registry.Descriptors()) != 1 {
		t.Fatalf("replacement snapshot provider=%#v exists=%v descriptors=%#v", provider, exists, registry.Descriptors())
	}
	if err := registry.Replace(nil); err == nil {
		t.Fatal("nil replacement was accepted")
	}
}

func TestRegistryReplaceProviderPreservesUnrelatedConcurrentUpdates(t *testing.T) {
	first := fakeProvider{descriptor: Descriptor{ID: "first", Type: ProviderOIDC, Interaction: InteractionBrowser}}
	second := fakeProvider{descriptor: Descriptor{ID: "second", Type: ProviderAnonymous, Interaction: InteractionNone}}
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	preparedFirst, _ := NewRegistry(first)
	preparedSecond, _ := NewRegistry(second)
	done := make(chan error, 2)
	go func() { done <- registry.ReplaceProvider(preparedFirst, "first", true) }()
	go func() { done <- registry.ReplaceProvider(preparedSecond, "second", true) }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(registry.Descriptors()) != 2 {
		t.Fatalf("descriptors = %#v", registry.Descriptors())
	}
	if err := registry.ReplaceProvider(preparedFirst, "first", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Provider("first"); ok {
		t.Fatal("disabled Provider remains installed")
	}
	if _, ok := registry.Provider("second"); !ok {
		t.Fatal("unrelated Provider was removed")
	}
}

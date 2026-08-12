package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStrictAuthenticationConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	data := `{"providers":[{"id":"corp","type":"oidc","displayName":"Corporate SSO","oidc":{"issuer":"https://login.example.test","clientId":"client","clientSecretFile":"/secret","redirectUrl":"https://gateway.example.test/auth/callback/corp"}}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Providers) != 1 || config.Providers[0].ID != "corp" || config.Providers[0].OIDC == nil {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadADAuthenticationConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	data := `{"providers":[{"id":"legacy-ad","type":"ad","displayName":"Corporate AD","ad":{"directoryId":"corp","url":"ldaps://dc.example.test:636","baseDn":"DC=example,DC=test","bindDn":"CN=reader,DC=example,DC=test","bindPasswordFile":"/secret/bind","caFile":"/secret/ca.crt","nestedGroupDepth":1,"maxGroups":100}}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].AD == nil ||
		loaded.Providers[0].AD.DirectoryID != "corp" || loaded.Providers[0].AD.NestedGroupDepth != 1 {
		t.Fatalf("AD config = %#v", loaded)
	}
}

func TestLoadRejectsUnknownFieldsAndTrailingDocuments(t *testing.T) {
	for name, data := range map[string]string{
		"unknown":  `{"providers":[],"secret":"leak"}`,
		"trailing": `{"providers":[]} {"providers":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected strict decode failure")
			}
		})
	}
}

func TestBuildRejectsUnknownOrIncompleteProvider(t *testing.T) {
	if _, err := Build(t.Context(), File{Providers: []Provider{{ID: "unknown", Type: "saml"}}}); err == nil {
		t.Fatal("expected unknown provider rejection")
	}
	if _, err := Build(t.Context(), File{Providers: []Provider{{ID: "corp", Type: "oidc"}}}); err == nil {
		t.Fatal("expected missing OIDC config rejection")
	}
	if _, err := Build(t.Context(), File{Providers: []Provider{{ID: "ad", Type: "ad"}}}); err == nil {
		t.Fatal("expected missing AD config rejection")
	}
}

func TestBuildDevelopmentProvidersRequireExplicitMode(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "static-token")
	if err := os.WriteFile(tokenPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	staticProvider := Provider{
		ID: "local", Type: "static-token",
		StaticToken: &StaticTokenConfig{
			TokenFile:                 tokenPath,
			DevelopmentIdentityConfig: DevelopmentIdentityConfig{Subject: "developer", Groups: []string{"developers"}},
		},
	}
	if _, err := Build(t.Context(), File{Providers: []Provider{staticProvider}}); err == nil {
		t.Fatal("static-token was enabled without developmentMode")
	}
	registry, err := Build(t.Context(), File{DevelopmentMode: true, Providers: []Provider{
		staticProvider,
		{ID: "guest", Type: "anonymous", Anonymous: &AnonymousConfig{
			DevelopmentIdentityConfig: DevelopmentIdentityConfig{Subject: "guest"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 2 || descriptors[0].Type != "static-token" || descriptors[0].Interaction != "token" ||
		descriptors[1].Type != "anonymous" || descriptors[1].Interaction != "none" {
		t.Fatalf("development descriptors = %#v", descriptors)
	}
}

func TestBuildRejectsIncompleteDevelopmentProviders(t *testing.T) {
	for _, provider := range []Provider{
		{ID: "token", Type: "static-token"},
		{ID: "anonymous", Type: "anonymous"},
	} {
		if _, err := Build(t.Context(), File{DevelopmentMode: true, Providers: []Provider{provider}}); err == nil {
			t.Fatalf("incomplete provider %#v was accepted", provider)
		}
	}
}

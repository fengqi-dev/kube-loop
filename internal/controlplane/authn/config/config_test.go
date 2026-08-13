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
}

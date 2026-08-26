package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/auth"
)

func TestEnsureBuiltinOAuthClientsUsesDesktopProtocolCallback(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	now := time.Now().UTC()
	if err := store.OAuthClients().Create(t.Context(), OAuthClient{
		ID: auth.DesktopClientID, Name: "Old Desktop", Public: true,
		RedirectURIs: []string{"http://127.0.0.1/callback"},
		GrantTypes:   []string{grantAuthorizationCode, grantRefreshToken},
		Scopes:       []string{scopeOpenID, scopeOfflineAccess},
		Trusted:      true, Enabled: true, Builtin: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureBuiltinOAuthClients(
		t.Context(),
		store.OAuthClients(),
		"https://gateway.example/callback",
	); err != nil {
		t.Fatal(err)
	}
	desktop, err := store.OAuthClients().Get(t.Context(), auth.DesktopClientID)
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.RedirectURIs) != 1 ||
		desktop.RedirectURIs[0] != auth.DesktopRedirectURI {
		t.Fatalf("desktop redirect URIs = %v", desktop.RedirectURIs)
	}
}

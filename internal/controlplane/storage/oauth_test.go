package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOAuthClientRepositoryLifecycleAndSecretIsolation(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Hour)
	client := OAuthClient{
		ID: "client-lifecycle", Name: "Lifecycle Client", GrantTypes: []string{"client_credentials"},
		Scopes: []string{scopeKubeLoopAPI}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.OAuthClients().Create(ctx, client); err != nil {
		t.Fatal(err)
	}
	items, err := store.OAuthClients().List(ctx)
	if err != nil || len(items) != 1 || items[0].ID != client.ID {
		t.Fatalf("OAuth clients=%#v error=%v", items, err)
	}
	stored, err := store.OAuthClients().Get(ctx, client.ID)
	if err != nil || stored.Name != client.Name || stored.Public {
		t.Fatalf("stored OAuth client=%#v error=%v", stored, err)
	}

	client.Name = "Updated Client"
	client.Enabled = false
	client.UpdatedAt = now.Add(time.Minute)
	if err := store.OAuthClients().Update(ctx, client); err != nil {
		t.Fatal(err)
	}
	stored, err = store.OAuthClients().Get(ctx, client.ID)
	if err != nil || stored.Name != client.Name || stored.Enabled {
		t.Fatalf("updated OAuth client=%#v error=%v", stored, err)
	}

	firstHash := bytes.Repeat([]byte{1}, 32)
	secondHash := bytes.Repeat([]byte{2}, 32)
	if err := store.OAuthClients().SetSecret(ctx, OAuthClientSecret{
		ClientID: client.ID, SecretHash: firstHash, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.OAuthClients().SetSecret(ctx, OAuthClientSecret{
		ClientID: client.ID, SecretHash: secondHash, CreatedAt: now, UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	secret, err := store.OAuthClients().GetSecret(ctx, client.ID)
	if err != nil || !bytes.Equal(secret.SecretHash, secondHash) || !secret.CreatedAt.Equal(now) {
		t.Fatalf("OAuth client secret=%#v error=%v", secret, err)
	}

	if err := store.OAuthClients().Delete(ctx, client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OAuthClients().Get(ctx, client.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted OAuth client error=%v", err)
	}
	if _, err := store.OAuthClients().GetSecret(ctx, client.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cascaded OAuth client secret error=%v", err)
	}
}

func TestOAuthSessionRepositoryLifecycle(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Hour)
	identityID, clientID := createOAuthRepositoryFixtures(t, store, now)

	activeHash := bytes.Repeat([]byte{1}, 32)
	activeRequest := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, OAuthSession{
		Kind: "access_token", SignatureHash: activeHash, RequestID: activeRequest,
		IdentityID: identityID, ClientID: clientID, DeviceID: "device-a",
		RequestJSON: json.RawMessage(`{"granted_scopes":["openid"]}`), Status: statusActive,
		CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.OAuthSessions().Get(ctx, "access_token", activeHash)
	if err != nil || stored.RequestID != activeRequest || stored.Status != statusActive {
		t.Fatalf("OAuth session=%#v error=%v", stored, err)
	}
	owner, device, err := store.OAuthSessions().RequestOwner(ctx, activeRequest)
	if err != nil || owner != identityID || device != "device-a" {
		t.Fatalf("OAuth owner=%q device=%q error=%v", owner, device, err)
	}
	active, err := store.OAuthSessions().RequestActive(ctx, activeRequest, now)
	if err != nil || !active {
		t.Fatalf("OAuth request active=%v error=%v", active, err)
	}
	consumed, err := store.OAuthSessions().Consume(ctx, "access_token", activeHash, now)
	if err != nil || consumed.Status != "consumed" {
		t.Fatalf("consumed OAuth session=%#v error=%v", consumed, err)
	}
	active, err = store.OAuthSessions().RequestActive(ctx, activeRequest, now)
	if err != nil || active {
		t.Fatalf("consumed OAuth request active=%v error=%v", active, err)
	}

	revokedHash := bytes.Repeat([]byte{2}, 32)
	revokedRequest := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, OAuthSession{
		Kind: grantRefreshToken, SignatureHash: revokedHash, RequestID: revokedRequest,
		IdentityID: identityID, ClientID: clientID, DeviceID: "device-a",
		RequestJSON: json.RawMessage(`{"requested_scopes":["openid"]}`),
		Status:      statusActive, CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	revokedAt := now.Add(time.Minute)
	if err := store.OAuthSessions().RevokeRequest(ctx, revokedRequest, revokedAt); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.OAuthSessions().Get(ctx, grantRefreshToken, revokedHash)
	if err != nil || revoked.Status != "revoked" || revoked.RevokedAt == nil {
		t.Fatalf("revoked OAuth session=%#v error=%v", revoked, err)
	}

	identityHash := bytes.Repeat([]byte{3}, 32)
	if err := store.OAuthSessions().Create(ctx, OAuthSession{
		Kind: "access_token", SignatureHash: identityHash, RequestID: uuid.NewString(),
		IdentityID: identityID, ClientID: clientID, DeviceID: "device-a",
		RequestJSON: json.RawMessage(`{}`), Status: statusActive,
		CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	count, err := store.OAuthSessions().RevokeIdentity(ctx, identityID, revokedAt)
	if err != nil || count != 1 {
		t.Fatalf("revoked identity count=%d error=%v", count, err)
	}

	expiredHash := bytes.Repeat([]byte{4}, 32)
	if err := store.OAuthSessions().Create(ctx, OAuthSession{
		Kind: "access_token", SignatureHash: expiredHash, RequestID: uuid.NewString(),
		IdentityID: identityID, ClientID: clientID, DeviceID: "device-a",
		RequestJSON: json.RawMessage(`{}`), Status: statusActive,
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	count, err = store.OAuthSessions().DeleteExpired(ctx, now, 10)
	if err != nil || count != 1 {
		t.Fatalf("deleted expired OAuth sessions=%d error=%v", count, err)
	}
	if _, err := store.OAuthSessions().Get(ctx, "access_token", expiredHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired OAuth session error=%v", err)
	}
	if err := store.OAuthSessions().Delete(ctx, "access_token", activeHash); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthConsentRepositoryLifecycle(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	ctx := t.Context()
	now := time.Now().UTC().Add(-time.Hour)
	identityID, clientID := createOAuthRepositoryFixtures(t, store, now)
	scopeHash := bytes.Repeat([]byte{7}, 32)
	consent := OAuthConsent{
		IdentityID: identityID, ClientID: clientID, ScopeHash: scopeHash,
		Scopes: []string{scopeOpenID}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.OAuthConsents().Grant(ctx, consent); err != nil {
		t.Fatal(err)
	}
	has, err := store.OAuthConsents().Has(ctx, identityID, clientID, scopeHash)
	if err != nil || !has {
		t.Fatalf("OAuth consent has=%v error=%v", has, err)
	}
	consent.Scopes = []string{scopeOpenID, scopeKubeLoopAPI}
	consent.UpdatedAt = now.Add(time.Minute)
	if err := store.OAuthConsents().Grant(ctx, consent); err != nil {
		t.Fatal(err)
	}
	if err := store.OAuthConsents().RevokeClient(ctx, identityID, clientID); err != nil {
		t.Fatal(err)
	}
	has, err = store.OAuthConsents().Has(ctx, identityID, clientID, scopeHash)
	if err != nil || has {
		t.Fatalf("revoked OAuth consent has=%v error=%v", has, err)
	}
}

func createOAuthRepositoryFixtures(t *testing.T, store *Store, now time.Time) (string, string) {
	t.Helper()
	identityID := uuid.NewString()
	if _, err := store.Identities().Create(t.Context(), Identity{
		ID: identityID, Type: identityTypeHuman, DisplayName: "OAuth Owner", Status: statusActive,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	clientID := "oauth-fixture"
	if err := store.OAuthClients().Create(t.Context(), OAuthClient{
		ID: clientID, Name: "OAuth Fixture", Public: true,
		RedirectURIs: []string{"http://127.0.0.1/callback"}, GrantTypes: []string{grantAuthorizationCode},
		Scopes: []string{scopeOpenID, scopeKubeLoopAPI}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return identityID, clientID
}

func TestOAuthSessionRepositoryListsGrantsWithoutTokenMaterial(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "kubeloop.db"))
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	identityID := uuid.NewString()
	if _, err := store.Identities().Create(ctx, Identity{
		ID: identityID, Type: identityTypeHuman, DisplayName: "Grant Owner", Status: statusActive,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.OAuthClients().Create(ctx, OAuthClient{
		ID: "client-a", Name: "Client A", Public: true,
		RedirectURIs: []string{"http://127.0.0.1/callback"}, GrantTypes: []string{grantAuthorizationCode},
		Scopes: []string{scopeOpenID, scopeKubeLoopAPI}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	requestIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for index, requestID := range requestIDs {
		if err := store.OAuthSessions().Create(ctx, OAuthSession{
			Kind:          grantRefreshToken,
			SignatureHash: bytes.Repeat([]byte{byte(index + 1)}, 32),
			RequestID:     requestID,
			IdentityID:    identityID,
			ClientID:      "client-a",
			DeviceID:      "device-a",
			RequestJSON:   json.RawMessage(`{"granted_scopes":["openid","kubeloop.api"],"secret":"must-not-leak"}`),
			Status:        statusActive,
			CreatedAt:     now.Add(time.Duration(index) * time.Minute),
			ExpiresAt:     now.Add(2 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.OAuthSessions().ListGrants(ctx, OAuthGrantListFilter{
		IdentityID: identityID, ClientID: "client-a", Status: statusActive, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].RequestID != requestIDs[2] ||
		items[1].RequestID != requestIDs[1] {
		t.Fatalf("first page=%+v", items)
	}
	if len(items[0].Scopes) != 2 || items[0].Scopes[0] != scopeKubeLoopAPI ||
		items[0].Scopes[1] != scopeOpenID {
		t.Fatalf("grant scopes=%v", items[0].Scopes)
	}
	second, err := store.OAuthSessions().ListGrants(ctx, OAuthGrantListFilter{
		IdentityID: identityID, Limit: 2, Cursor: pageCursor(items[1].CreatedAt, items[1].RequestID),
	})
	if err != nil || len(second) != 1 || second[0].RequestID != requestIDs[0] {
		t.Fatalf("second page=%+v error=%v", second, err)
	}
}

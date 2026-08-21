package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

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

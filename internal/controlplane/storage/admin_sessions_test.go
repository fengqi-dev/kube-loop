package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAdminSessionRepositoryLifecycleAndOptimisticTouch(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "admin-sessions.db"))
	testAdminSessionRepositoryConformance(t, store)
}

func testAdminSessionRepositoryConformance(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)
	identityID, authorizationID := seedAdminSessionIdentity(t, store, now)
	idHash := sha256.Sum256([]byte("management-session-id"))
	csrfHash := sha256.Sum256([]byte("management-csrf-token"))
	session := AdminSession{
		IDHash: idHash[:], IdentityID: identityID, AuthorizationID: authorizationID, AuthenticationType: "normal",
		CSRFTokenHash: csrfHash[:], CreatedAt: now, LastSeenAt: now,
		IdleExpiresAt: now.Add(15 * time.Minute), AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}
	if err := store.AdminSessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	got, err := store.AdminSessions().GetByHash(ctx, idHash[:])
	if err != nil || got.IdentityID != identityID || got.AuthorizationID != authorizationID || got.AuthenticationType != "normal" ||
		got.SchemaVersion != ObjectSchemaVersion || string(got.CSRFTokenHash) != string(csrfHash[:]) {
		t.Fatalf("stored management session = %#v, error = %v", got, err)
	}
	nextSeen := now.Add(time.Minute)
	if err := store.AdminSessions().Touch(ctx, idHash[:], now, nextSeen, nextSeen, nextSeen.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.AdminSessions().Touch(ctx, idHash[:], now, nextSeen, nextSeen, nextSeen.Add(15*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale touch error = %v", err)
	}
	revokedAt := nextSeen.Add(time.Minute)
	if err := store.AdminSessions().Revoke(ctx, idHash[:], revokedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.AdminSessions().Revoke(ctx, idHash[:], revokedAt.Add(time.Minute)); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	got, err = store.AdminSessions().GetByHash(ctx, idHash[:])
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoked management session = %#v, error = %v", got, err)
	}
	if err := store.AdminSessions().Touch(ctx, idHash[:], nextSeen, revokedAt, revokedAt, revokedAt.Add(15*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked touch error = %v", err)
	}
	deleted, err := store.AdminSessions().DeleteExpired(ctx, revokedAt.Add(time.Minute), 10)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted management sessions = %d, error = %v", deleted, err)
	}
}

func TestBreakGlassAdminSessionRequiresGenerationAndNoIdentity(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "break-glass-session.db"))
	now := time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC)
	idHash := sha256.Sum256([]byte("break-glass-session"))
	csrfHash := sha256.Sum256([]byte("break-glass-csrf"))
	generationHash := sha256.Sum256([]byte("rotated-secret"))
	session := AdminSession{
		IDHash: idHash[:], AuthenticationType: "break-glass",
		BreakGlassGeneration: base64.RawURLEncoding.EncodeToString(generationHash[:]), CSRFTokenHash: csrfHash[:],
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(10 * time.Minute), AbsoluteExpiresAt: now.Add(10 * time.Minute),
	}
	if err := store.AdminSessions().Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	stored, err := store.AdminSessions().GetByHash(context.Background(), idHash[:])
	if err != nil || stored.IdentityID != "" || stored.AuthorizationID != "" || stored.BreakGlassGeneration != session.BreakGlassGeneration {
		t.Fatalf("stored break-glass session = %#v, error = %v", stored, err)
	}
	invalid := session
	invalid.IDHash = append([]byte(nil), idHash[:]...)
	invalid.IDHash[0]++
	invalid.IdentityID = uuid.NewString()
	if err := store.AdminSessions().Create(context.Background(), invalid); err == nil {
		t.Fatal("break-glass session with a Identity succeeded")
	}
	invalid = session
	invalid.IDHash = append([]byte(nil), idHash[:]...)
	invalid.IDHash[0] += 2
	invalid.BreakGlassGeneration = "not-a-generation"
	if err := store.AdminSessions().Create(context.Background(), invalid); err == nil {
		t.Fatal("break-glass session with invalid generation succeeded")
	}
}

func TestAdminSessionRepositoryRejectsInvalidHashesAndLifetime(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "invalid-admin-session.db"))
	now := time.Now().UTC()
	identityID, authorizationID := seedAdminSessionIdentity(t, store, now)
	validHash := make([]byte, sha256Size)
	tests := []AdminSession{
		{IDHash: []byte("short"), CSRFTokenHash: validHash, IdentityID: identityID, AuthorizationID: authorizationID, AuthenticationType: "normal", CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour)},
		{IDHash: validHash, CSRFTokenHash: []byte("short"), IdentityID: identityID, AuthorizationID: authorizationID, AuthenticationType: "normal", CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour)},
		{IDHash: validHash, CSRFTokenHash: validHash, AuthenticationType: "normal", CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour)},
		{IDHash: validHash, CSRFTokenHash: validHash, IdentityID: identityID, AuthorizationID: authorizationID, AuthenticationType: "unknown", CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(time.Minute), AbsoluteExpiresAt: now.Add(time.Hour)},
		{IDHash: validHash, CSRFTokenHash: validHash, IdentityID: identityID, AuthorizationID: authorizationID, AuthenticationType: "normal", CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(2 * time.Hour), AbsoluteExpiresAt: now.Add(time.Hour)},
	}
	for index, session := range tests {
		session.IDHash = append([]byte(nil), session.IDHash...)
		if len(session.IDHash) == sha256Size {
			session.IDHash[0] = byte(index + 1)
		}
		if err := store.AdminSessions().Create(context.Background(), session); err == nil {
			t.Fatalf("invalid management session %d succeeded", index)
		}
	}
}

func seedAdminSessionIdentity(t *testing.T, store *Store, now time.Time) (string, string) {
	t.Helper()
	identityID := uuid.NewString()
	if _, err := store.Identities().Create(context.Background(), Identity{
		ID: identityID, Type: "human", DisplayName: "Test User", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	signatureHash := sha256.Sum256([]byte("access-" + authorizationID))
	if err := store.OAuthSessions().Create(context.Background(), OAuthSession{
		Kind: "access_token", SignatureHash: signatureHash[:], RequestID: authorizationID,
		IdentityID: identityID, ClientID: "kubeloop-management", DeviceID: "management-device",
		RequestJSON: json.RawMessage(`{}`), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return identityID, authorizationID
}

package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func TestAuthenticateSubjectUsesIdentityAndOAuthGrantRevocation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 11, 30, 0, 0, time.UTC)
	identity, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
		Kind: "access_token", SignatureHash: bytes.Repeat([]byte{8}, 32), RequestID: authorizationID,
		RequestJSON: []byte(`{}`), Status: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	token := opaqueToken(10)
	tokenHash := sha256.Sum256([]byte(token))
	if err := store.AdminSessions().Create(ctx, storage.AdminSession{
		IDHash: tokenHash[:], IdentityID: identity.ID, AuthorizationID: authorizationID,
		AuthenticationType: string(adminauthentication.Normal), CSRFTokenHash: bytes.Repeat([]byte{9}, 32),
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(15 * time.Minute), AbsoluteExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	service, _ := New(store)
	service.now = func() time.Time { return now }
	_, subject, err := service.AuthenticateSubject(ctx, token)
	if err != nil || subject.ID != identity.ID {
		t.Fatalf("subject=%#v error=%v", subject, err)
	}
	if err := store.OAuthSessions().RevokeRequest(ctx, authorizationID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.AuthenticateSubject(ctx, token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked OAuth grant authentication error = %v", err)
	}
}

func TestIdentityExchangePersistsDedicatedSessionAndAudit(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 11, 45, 0, 0, time.UTC)
	identity, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
		Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{12}, 32), RequestID: authorizationID,
		RequestJSON: []byte(`{}`), Status: "active", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	service, _ := New(store)
	service.now = func() time.Time { return now }
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{13}, 32), bytes.Repeat([]byte{14}, 32)...))
	issued, err := service.ExchangeIdentity(
		ctx, identity.ID, authorizationID, adminauthentication.Normal, "request-identity-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ExpiresAt != now.Add(normalSessionAbsoluteTTL) {
		t.Fatalf("absolute expiry=%v", issued.ExpiresAt)
	}
	digest := sha256.Sum256([]byte(issued.SessionToken))
	stored, err := store.AdminSessions().GetByHash(ctx, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if stored.IdentityID != identity.ID || stored.AuthorizationID != authorizationID ||
		stored.AuthenticationType != "normal" || stored.IdleExpiresAt != now.Add(normalSessionIdleTTL) {
		t.Fatalf("stored identity session=%+v", stored)
	}
	if err := VerifyCSRF(stored, issued.CSRFToken); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := service.Authenticate(ctx, issued.SessionToken); err != nil {
		t.Fatal(err)
	}
	stored, err = store.AdminSessions().GetByHash(ctx, digest[:])
	if err != nil || stored.IdleExpiresAt != now.Add(2*time.Minute+normalSessionIdleTTL) {
		t.Fatalf("sliding idle session=%+v error=%v", stored, err)
	}
	events, err := store.Audit().List(ctx, storage.AuditFilter{Action: identityExchangeAudit})
	if err != nil || len(events) != 1 || events[0].IdentityID != identity.ID || events[0].RequestID != "request-identity-1" {
		t.Fatalf("identity exchange audit=%+v error=%v", events, err)
	}
	if err := store.OAuthSessions().RevokeRequest(ctx, authorizationID, now); err != nil {
		t.Fatal(err)
	}
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{15}, 32), bytes.Repeat([]byte{16}, 32)...))
	if _, err := service.ExchangeIdentity(
		ctx, identity.ID, authorizationID, adminauthentication.Normal, "request-identity-2",
	); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("revoked OAuth grant exchange error=%v", err)
	}
}

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func opaqueToken(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

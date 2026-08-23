package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestRevokeInvalidatesSessionAndAuditsWithoutTokens(t *testing.T) {
	ctx := t.Context()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	identity, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: statusActive,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
		Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{17}, 32), RequestID: authorizationID,
		RequestJSON: []byte(`{}`), Status: statusActive, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.random = bytes.NewReader(
		append(bytes.Repeat([]byte{18}, 32), bytes.Repeat([]byte{19}, 32)...),
	)
	issued, err := service.ExchangeIdentity(
		ctx, identity.ID, authorizationID, adminauthentication.Normal, "request-exchange",
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(issued.SessionToken))
	stored, err := store.AdminSessions().GetByHash(ctx, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(time.Minute) }
	if err := service.Revoke(ctx, stored, " request-revoke "); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, issued.SessionToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked session authentication error = %v", err)
	}
	events, err := store.Audit().List(ctx, storage.AuditFilter{Action: sessionRevokeAudit})
	if err != nil || len(events) != 1 || events[0].IdentityID != identity.ID ||
		events[0].RequestID != "request-revoke" {
		t.Fatalf("session revoke audit = %+v, error = %v", events, err)
	}
	metadata := string(events[0].Metadata)
	if strings.Contains(metadata, issued.SessionToken) || strings.Contains(metadata, issued.CSRFToken) {
		t.Fatal("session revoke audit contains plaintext credentials")
	}
	if err := service.Revoke(ctx, stored, "request-revoke-again"); err != nil {
		t.Fatalf("idempotent revoke error = %v", err)
	}
	events, err = store.Audit().List(ctx, storage.AuditFilter{Action: sessionRevokeAudit})
	if err != nil || len(events) != 2 {
		t.Fatalf("idempotent revoke audits = %+v, error = %v", events, err)
	}
	if err := service.Revoke(ctx, storage.AdminSession{}, "request-invalid"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalid session revoke error = %v", err)
	}
}

package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestAuthenticateSubjectUsesIdentityAndOAuthGrantRevocation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 11, 30, 0, 0, time.UTC)
	identity, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: statusActive,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
		Kind: "access_token", SignatureHash: bytes.Repeat([]byte{8}, 32), RequestID: authorizationID,
		RequestJSON: []byte(`{}`), Status: statusActive, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	token := opaqueToken(10)
	tokenHash := sha256.Sum256([]byte(token))
	if err := store.AdminSessions().Create(ctx, storage.AdminSession{
		IDHash: tokenHash[:], IdentityID: identity.ID, AuthorizationID: authorizationID,
		AuthenticationType: string(adminauthentication.Normal), CSRFTokenHash: bytes.Repeat([]byte{9}, 32),
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(15 * time.Minute),
		AbsoluteExpiresAt: now.Add(time.Hour),
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

package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func TestBreakGlassExchangePersistsOnlyHashesAndAudit(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	verifier := &fakeBreakGlassVerifier{enabled: true, generation: testGeneration(1), ttl: 10 * time.Minute}
	service, err := New(store, verifier)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)...))
	service.newID = func() string { return "070cc2ce-0727-44fa-9f4e-285c78ef1eb5" }
	credential := []byte("valid")

	issued, err := service.ExchangeBreakGlass(ctx, netip.MustParseAddr("192.0.2.10"), credential, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if issued.ExpiresAt != now.Add(10*time.Minute) || !allZero(credential) {
		t.Fatalf("issued=%+v credentialCleared=%v", issued, allZero(credential))
	}
	sessionDigest := sha256.Sum256([]byte(issued.SessionToken))
	stored, err := store.AdminSessions().GetByHash(ctx, sessionDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.IDHash, []byte(issued.SessionToken)) || bytes.Contains(stored.CSRFTokenHash, []byte(issued.CSRFToken)) {
		t.Fatal("plaintext management credential reached storage")
	}
	if err := VerifyCSRF(stored, issued.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCSRF(stored, opaqueToken(9)); !errors.Is(err, ErrCSRFInvalid) {
		t.Fatalf("wrong CSRF error = %v", err)
	}
	events, err := store.Audit().List(ctx, storage.AuditFilter{Action: breakGlassExchangeAudit})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].RequestID != "request-1" || bytes.Contains(events[0].Metadata, []byte(issued.SessionToken)) {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestBreakGlassSecretRotationInvalidatesIssuedSession(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	verifier := &fakeBreakGlassVerifier{enabled: true, generation: testGeneration(4), ttl: 5 * time.Minute}
	service, _ := New(store, verifier)
	service.now = func() time.Time { return now }
	issued, err := service.ExchangeBreakGlass(context.Background(), netip.Addr{}, []byte("valid"), "request-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), issued.SessionToken); err != nil {
		t.Fatal(err)
	}
	verifier.generation = testGeneration(5)
	if _, err := service.Authenticate(context.Background(), issued.SessionToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("rotated session error = %v", err)
	}
}

func TestAuthenticateSubjectUsesCurrentGroupsAndOAuthGrantRevocation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 11, 30, 0, 0, time.UTC)
	principal, err := store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: "management-user",
		Groups: []string{"auditors"}, CreatedAt: now, UpdatedAt: now,
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
		IDHash: tokenHash[:], PrincipalID: principal.ID, AuthorizationID: authorizationID,
		AuthenticationType: string(adminauthorization.AuthenticationNormal), CSRFTokenHash: bytes.Repeat([]byte{9}, 32),
		CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(15 * time.Minute), AbsoluteExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	service, _ := New(store, &fakeBreakGlassVerifier{})
	service.now = func() time.Time { return now }
	if _, err := store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: principal.Provider, ExternalID: principal.ExternalID,
		Groups: []string{"security"}, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	_, subject, err := service.AuthenticateSubject(ctx, token)
	if err != nil || subject.ID != principal.ID || len(subject.Groups) != 1 || subject.Groups[0] != "security" {
		t.Fatalf("subject=%#v error=%v", subject, err)
	}
	if err := store.OAuthSessions().RevokeRequest(ctx, authorizationID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.AuthenticateSubject(ctx, token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked OAuth grant authentication error = %v", err)
	}
}

func TestPrincipalExchangePersistsDedicatedSessionAndAudit(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 11, 45, 0, 0, time.UTC)
	principal, err := store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: "admin-user",
		Groups: []string{"platform"}, CreatedAt: now, UpdatedAt: now,
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
	service, _ := New(store, &fakeBreakGlassVerifier{})
	service.now = func() time.Time { return now }
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{13}, 32), bytes.Repeat([]byte{14}, 32)...))
	issued, err := service.ExchangePrincipal(
		ctx, principal.ID, authorizationID, adminauthorization.AuthenticationNormal, "request-principal-1",
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
	if stored.PrincipalID != principal.ID || stored.AuthorizationID != authorizationID ||
		stored.AuthenticationType != "normal" || stored.IdleExpiresAt != now.Add(normalSessionIdleTTL) {
		t.Fatalf("stored principal session=%+v", stored)
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
	events, err := store.Audit().List(ctx, storage.AuditFilter{Action: principalExchangeAudit})
	if err != nil || len(events) != 1 || events[0].PrincipalID != principal.ID || events[0].RequestID != "request-principal-1" {
		t.Fatalf("principal exchange audit=%+v error=%v", events, err)
	}
	if err := store.OAuthSessions().RevokeRequest(ctx, authorizationID, now); err != nil {
		t.Fatal(err)
	}
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{15}, 32), bytes.Repeat([]byte{16}, 32)...))
	if _, err := service.ExchangePrincipal(
		ctx, principal.ID, authorizationID, adminauthorization.AuthenticationNormal, "request-principal-2",
	); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("revoked OAuth grant exchange error=%v", err)
	}
}

func TestAuditFailureRollsBackBreakGlassSession(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	eventID := uuid.NewString()
	if err := store.Audit().Append(ctx, storage.AuditEvent{
		ID: eventID, Action: "seed", Outcome: "success", RequestID: "seed", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service, _ := New(store, &fakeBreakGlassVerifier{enabled: true, generation: testGeneration(6), ttl: 5 * time.Minute})
	service.now = func() time.Time { return now }
	service.newID = func() string { return eventID }
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)...))

	_, err := service.ExchangeBreakGlass(ctx, netip.Addr{}, []byte("valid"), "request-3")
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("exchange error = %v", err)
	}
	digest := sha256.Sum256([]byte(opaqueToken(7)))
	if _, err := store.AdminSessions().GetByHash(ctx, digest[:]); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("rolled-back session lookup error = %v", err)
	}
}

func TestBreakGlassExchangeRejectsInvalidLifetimeAndEntropyFailure(t *testing.T) {
	store := openTestStore(t)
	verifier := &fakeBreakGlassVerifier{enabled: true, generation: testGeneration(2), ttl: 16 * time.Minute}
	service, _ := New(store, verifier)
	if _, err := service.ExchangeBreakGlass(context.Background(), netip.Addr{}, []byte("valid"), "request-4"); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("long lifetime error = %v", err)
	}
	verifier.ttl = time.Minute
	service.random = errorReader{}
	if _, err := service.ExchangeBreakGlass(context.Background(), netip.Addr{}, []byte("valid"), "request-5"); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("entropy failure error = %v", err)
	}
}

func TestRejectedBreakGlassAttemptIsAuditedWithoutReasonOrCredential(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	service, _ := New(store, &fakeBreakGlassVerifier{enabled: true, generation: testGeneration(2), ttl: time.Minute})
	credential := []byte("wrong")
	if _, err := service.ExchangeBreakGlass(ctx, netip.Addr{}, credential, "request-rejected"); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("exchange error = %v", err)
	}
	events, err := store.Audit().List(ctx, storage.AuditFilter{Action: breakGlassExchangeAudit})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Outcome != "failure" ||
		bytes.Contains(events[0].Metadata, []byte("wrong")) || bytes.Contains(events[0].Metadata, []byte("reason")) {
		t.Fatalf("failure audit = %+v", events)
	}
}

type fakeBreakGlassVerifier struct {
	enabled    bool
	generation string
	ttl        time.Duration
}

func (verifier *fakeBreakGlassVerifier) Verify(_ context.Context, _ netip.Addr, supplied []byte) (string, error) {
	defer clear(supplied)
	if !verifier.enabled || !bytes.Equal(supplied, []byte("valid")) {
		return "", ErrAuthenticationFailed
	}
	return verifier.generation, nil
}

func (verifier *fakeBreakGlassVerifier) SessionTTL() time.Duration { return verifier.ttl }

func (verifier *fakeBreakGlassVerifier) CurrentBreakGlassState(context.Context) (adminauthorization.BreakGlassState, error) {
	return adminauthorization.BreakGlassState{Enabled: verifier.enabled, Generation: verifier.generation}, nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

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

func testGeneration(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func opaqueToken(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

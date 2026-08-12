package token

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

func TestTokenIssueRotationReuseDetectionAndAuthentication(t *testing.T) {
	service, principal, store, setNow := newTokenTestService(t)
	pair, err := service.Issue(context.Background(), principal, "desktop-1")
	if err != nil {
		t.Fatal(err)
	}
	if pair.TokenType != "Bearer" || pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("pair = %#v", pair)
	}
	identity, err := service.Authenticate(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Principal.ID != principal.ID || identity.DeviceID != "desktop-1" || identity.FamilyID == "" ||
		!identity.AccessExpiresAt.Equal(pair.AccessExpiresAt) {
		t.Fatalf("access identity = %#v", identity)
	}
	family, err := store.TokenFamilies().GetByID(context.Background(), identity.FamilyID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(family.RefreshTokenHash), pair.RefreshToken) {
		t.Fatal("raw refresh token was persisted")
	}

	setNow(time.Date(2026, 8, 9, 11, 1, 0, 0, time.UTC))
	rotated, err := service.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Authenticate(context.Background(), rotated.AccessToken); err != nil {
		t.Fatalf("rotated access token = %v", err)
	}

	if _, err := service.Refresh(context.Background(), pair.RefreshToken); !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("old refresh token reuse = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), rotated.AccessToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("access token after family reuse = %v", err)
	}
	if _, err := service.Refresh(context.Background(), rotated.RefreshToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("rotated refresh after family reuse = %v", err)
	}
}

func TestTokenRejectsTamperingExpiryAndExplicitRevocation(t *testing.T) {
	service, principal, _, setNow := newTokenTestService(t)
	pair, err := service.Issue(context.Background(), principal, "desktop-2")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(pair.AccessToken, ".")
	if len(parts) != 3 || len(parts[2]) < 4 {
		t.Fatalf("unexpected access token format")
	}
	index := len(parts[2]) / 2
	replacement := byte('A')
	if parts[2][index] == replacement {
		replacement = 'B'
	}
	parts[2] = parts[2][:index] + string(replacement) + parts[2][index+1:]
	tampered := strings.Join(parts, ".")
	if _, err := service.Authenticate(context.Background(), tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered access token = %v", err)
	}
	setNow(pair.AccessExpiresAt.Add(time.Minute))
	if _, err := service.Authenticate(context.Background(), pair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired access token = %v", err)
	}
	setNow(pair.AccessExpiresAt.Add(-time.Minute))
	if err := service.Revoke(context.Background(), pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), pair.AccessToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked access token = %v", err)
	}
}

func TestTokenRejectsJWTAlgorithmConfusion(t *testing.T) {
	service, _, _, _ := newTokenTestService(t)
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT","kid":"test-key"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker"}`)) + "."
	if _, err := service.Authenticate(context.Background(), unsigned); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("alg=none token error = %v", err)
	}
	hmacSigner, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.HS256, Key: []byte(service.publicKey)},
		new(jose.SignerOptions).WithType("JWT").WithHeader("kid", service.keyID),
	)
	if err != nil {
		t.Fatal(err)
	}
	confused, err := jwt.Signed(hmacSigner).Claims(map[string]any{"sub": "attacker"}).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), confused); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("HS256 confusion token error = %v", err)
	}
}

func TestConcurrentRefreshRevokesFamilyOnReplay(t *testing.T) {
	service, principal, _, _ := newTokenTestService(t)
	pair, err := service.Issue(context.Background(), principal, "desktop-3")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	type refreshResult struct {
		pair Pair
		err  error
	}
	results := make(chan refreshResult, 2)
	for range 2 {
		group.Go(func() {
			refreshed, err := service.Refresh(context.Background(), pair.RefreshToken)
			results <- refreshResult{pair: refreshed, err: err}
		})
	}
	group.Wait()
	close(results)
	var success, reuse int
	var successfulPair Pair
	for result := range results {
		switch {
		case result.err == nil:
			success++
			successfulPair = result.pair
		case errors.Is(result.err, ErrRefreshReuse):
			reuse++
		default:
			t.Fatalf("concurrent refresh error = %v", result.err)
		}
	}
	if success != 1 || reuse != 1 {
		t.Fatalf("concurrent refresh success=%d reuse=%d", success, reuse)
	}
	if _, err := service.Authenticate(context.Background(), successfulPair.AccessToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("concurrently replayed family remained active: %v", err)
	}
}

func TestTokenLifecycleSurvivesControlPlaneRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "tokens.db")
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 9, 11, 0, 0, 0, time.UTC)
	open := func() (*storage.Store, *Service) {
		t.Helper()
		store, err := storage.Open(ctx, storage.Config{
			Backend: storage.BackendSQLite, SQLitePath: databasePath,
		})
		if err != nil {
			t.Fatal(err)
		}
		service, err := New(store, Config{
			Issuer: "https://gateway.example.test", Audience: "kubeloop-api", KeyID: "test-key",
			SigningKey: privateKey, Now: func() time.Time { return now },
		})
		if err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		return store, service
	}

	store, service := open()
	principal, err := store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: "corporate", ExternalID: "https://identity.example.test\x00restart-subject",
		DisplayName: "Grace", Email: "grace@example.test", Groups: []string{"operators"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Issue(ctx, principal, "desktop-restart")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, service = open()
	if _, err := service.Authenticate(ctx, issued.AccessToken); err != nil {
		t.Fatalf("authenticate after restart: %v", err)
	}
	now = now.Add(time.Minute)
	rotated, err := service.Refresh(ctx, issued.RefreshToken)
	if err != nil {
		t.Fatalf("refresh after restart: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, service = open()
	if _, err := service.Refresh(ctx, issued.RefreshToken); !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("reuse after restart = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, service = open()
	t.Cleanup(func() { _ = store.Close() })
	if _, err := service.Authenticate(ctx, rotated.AccessToken); !errors.Is(err, ErrRevoked) {
		t.Fatalf("rotated access after persisted reuse revocation = %v", err)
	}
}

func newTokenTestService(t *testing.T) (*Service, storage.Principal, *storage.Store, func(time.Time)) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "tokens.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 9, 11, 0, 0, 0, time.UTC)
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "corporate", ExternalID: "https://identity.example.test\x00subject",
		DisplayName: "Ada", Email: "ada@example.test", Groups: []string{"developers"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var clockMu sync.RWMutex
	service, err := New(store, Config{
		Issuer: "https://gateway.example.test", Audience: "kubeloop-api", KeyID: "test-key",
		SigningKey: privateKey,
		Now: func() time.Time {
			clockMu.RLock()
			defer clockMu.RUnlock()
			return now
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	setNow := func(value time.Time) {
		clockMu.Lock()
		now = value
		clockMu.Unlock()
	}
	return service, principal, store, setNow
}

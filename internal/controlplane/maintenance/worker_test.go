package maintenance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestRunOnceDeletesExpiredSessionAndCascadesTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	store, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "maintenance.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	identity, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	expiredSession := storage.Session{
		ID: uuid.NewString(), IdentityID: identity.ID, DeviceID: "crashed-client", ClusterID: "cluster-a",
		Namespace: "development", State: "active", CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Minute), LastHeartbeatAt: now.Add(-time.Minute), ExpiresAt: now.Add(-time.Second),
	}
	network, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	expiredSession.NetworkSpec, err = networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	expiredSession.NetworkSpecHash, err = networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().Create(ctx, expiredSession); err != nil {
		t.Fatal(err)
	}
	expiry := expiredSession.ExpiresAt
	expiredTask := storage.Task{
		ID: uuid.NewString(), IdentityID: identity.ID, SessionID: expiredSession.ID,
		Type: "port-forward", State: remotetask.Running, Spec: json.RawMessage(`{"kind":"service"}`),
		IdempotencyKey: "crashed-port-forward", CreatedAt: now.Add(-time.Minute), ExpiresAt: &expiry,
	}
	if err := store.Tasks().Create(ctx, expiredTask); err != nil {
		t.Fatal(err)
	}

	activeSession := expiredSession
	activeSession.ID = uuid.NewString()
	activeSession.DeviceID = "healthy-client"
	activeSession.ExpiresAt = now.Add(time.Minute)
	if err := store.Sessions().Create(ctx, activeSession); err != nil {
		t.Fatal(err)
	}
	activeExpiry := activeSession.ExpiresAt
	activeTask := expiredTask
	activeTask.ID = uuid.NewString()
	activeTask.SessionID = activeSession.ID
	activeTask.IdempotencyKey = "healthy-port-forward"
	activeTask.ExpiresAt = &activeExpiry
	if err := store.Tasks().Create(ctx, activeTask); err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
		Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{3}, 32), RequestID: authorizationID,
		IdentityID: identity.ID, RequestJSON: []byte(`{}`), Status: "active", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	expiredAdminSession := storage.AdminSession{
		IDHash: bytes.Repeat([]byte{4}, 32), IdentityID: identity.ID, AuthorizationID: authorizationID, AuthenticationType: "normal",
		CSRFTokenHash: bytes.Repeat([]byte{5}, 32), CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Hour),
		IdleExpiresAt: now.Add(-time.Second), AbsoluteExpiresAt: now.Add(-time.Second),
	}
	if err := store.AdminSessions().Create(ctx, expiredAdminSession); err != nil {
		t.Fatal(err)
	}
	activeAdminSession := expiredAdminSession
	activeAdminSession.IDHash = bytes.Repeat([]byte{6}, 32)
	activeAdminSession.CSRFTokenHash = bytes.Repeat([]byte{7}, 32)
	activeAdminSession.IdleExpiresAt = now.Add(time.Minute)
	activeAdminSession.AbsoluteExpiresAt = now.Add(time.Minute)
	if err := store.AdminSessions().Create(ctx, activeAdminSession); err != nil {
		t.Fatal(err)
	}

	worker, err := maintenance.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.Config{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sessions != 1 || report.AdminSessions != 1 || report.Total() != 2 {
		t.Fatalf("maintenance report = %#v", report)
	}
	if _, err := store.Sessions().GetByID(ctx, expiredSession.ID); err != storage.ErrNotFound {
		t.Fatalf("expired Session lookup = %v", err)
	}
	if _, err := store.Tasks().GetByID(ctx, expiredTask.ID); err != storage.ErrNotFound {
		t.Fatalf("crashed client's Port Forward Task lookup = %v", err)
	}
	if _, err := store.Sessions().GetByID(ctx, activeSession.ID); err != nil {
		t.Fatalf("active Session lookup = %v", err)
	}
	if _, err := store.Tasks().GetByID(ctx, activeTask.ID); err != nil {
		t.Fatalf("active Port Forward Task lookup = %v", err)
	}
	if _, err := store.AdminSessions().GetByHash(ctx, expiredAdminSession.IDHash); err != storage.ErrNotFound {
		t.Fatalf("expired Management Session lookup = %v", err)
	}
	if _, err := store.AdminSessions().GetByHash(ctx, activeAdminSession.IDHash); err != nil {
		t.Fatalf("active Management Session lookup = %v", err)
	}
}

func TestConfigRejectsUnboundedCleanup(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "maintenance.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := maintenance.New(store, nil, maintenance.Config{BatchSize: 1001}); err == nil {
		t.Fatal("expected oversized maintenance batch to fail")
	}
}

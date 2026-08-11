package streamlease

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
)

func TestLeaseUsesEarliestAccessAndSessionExpiry(t *testing.T) {
	stateStore, principalID, sessionID, now := createLeaseStore(t)
	defer stateStore.Close()
	ctx, cancel, err := Start(context.Background(), stateStore, controller.Principal{
		Subject: principalID, DeviceID: "device", AccessExpiresAt: now.Add(30 * time.Millisecond),
	}, sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)}, Config{
		Now: func() time.Time { return now }, CheckInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
		if ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("lease error = %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("access token expiry did not terminate the lease")
	}
	if _, _, err := Start(context.Background(), stateStore, controller.Principal{}, sessionapi.ActiveSession{ExpiresAt: now}, Config{Now: func() time.Time { return now }}); err == nil {
		t.Fatal("expired Session produced a lease")
	}
}

func TestLeaseFollowsHeartbeatExtendedSessionExpiry(t *testing.T) {
	stateStore, principalID, sessionID, _ := createLeaseStore(t)
	defer stateStore.Close()
	stored, err := stateStore.Sessions().GetByID(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	initialExpiry := now.Add(100 * time.Millisecond)
	if err := stateStore.Sessions().Heartbeat(
		context.Background(), sessionID, stored.Generation, stored.NetworkSpec, stored.NetworkSpecHash, now, initialExpiry,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(context.Background(), stateStore, controller.Principal{
		Subject: principalID, DeviceID: "device",
	}, sessionapi.ActiveSession{ID: sessionID, ExpiresAt: initialExpiry}, Config{CheckInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	time.Sleep(30 * time.Millisecond)
	heartbeatAt := time.Now().UTC()
	if err := stateStore.Sessions().Heartbeat(
		context.Background(), sessionID, stored.Generation+1, stored.NetworkSpec, stored.NetworkSpecHash,
		heartbeatAt, heartbeatAt.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatalf("heartbeat-extended Session terminated its lease: %v", ctx.Err())
	case <-time.After(120 * time.Millisecond):
	}
}

func TestLeaseTerminatesAfterTokenFamilyRevocation(t *testing.T) {
	stateStore, principalID, sessionID, now := createLeaseStore(t)
	defer stateStore.Close()
	familyID := uuid.NewString()
	if err := stateStore.TokenFamilies().Create(context.Background(), storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: "device",
		RefreshTokenHash: bytes.Repeat([]byte{7}, 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(context.Background(), stateStore, controller.Principal{
		Subject: principalID, DeviceID: "device", FamilyID: familyID,
	}, sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)}, Config{CheckInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := stateStore.TokenFamilies().Revoke(context.Background(), familyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(t, ctx, "revoked Token Family")
}

func TestFamilyBackedLeaseOutlivesOpeningAccessToken(t *testing.T) {
	stateStore, principalID, sessionID, now := createLeaseStore(t)
	defer stateStore.Close()
	familyID := uuid.NewString()
	if err := stateStore.TokenFamilies().Create(context.Background(), storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: "device",
		RefreshTokenHash: bytes.Repeat([]byte{8}, 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(context.Background(), stateStore, controller.Principal{
		Subject: principalID, DeviceID: "device", FamilyID: familyID, AccessExpiresAt: time.Now().Add(30 * time.Millisecond),
	}, sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)}, Config{CheckInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("opening access-token expiry terminated a Family-backed lease: %v", ctx.Err())
	case <-time.After(60 * time.Millisecond):
	}
	if err := stateStore.TokenFamilies().Revoke(context.Background(), familyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(t, ctx, "revoked Token Family after access-token refresh")
}

func TestLeaseTerminatesAfterSessionStops(t *testing.T) {
	stateStore, principalID, sessionID, now := createLeaseStore(t)
	defer stateStore.Close()
	ctx, cancel, err := Start(context.Background(), stateStore, controller.Principal{
		Subject: principalID, DeviceID: "device",
	}, sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)}, Config{CheckInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := stateStore.Sessions().UpdateState(context.Background(), sessionID, 1, "stopped", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(t, ctx, "stopped Session")
}

func createLeaseStore(t *testing.T) (*storage.Store, string, string, time.Time) {
	t.Helper()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "lease.db"), ControllerReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(context.Background(), storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "lease-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}})
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore, principalID, sessionID, now
}

func waitForCancellation(t *testing.T, ctx context.Context, reason string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("%s did not terminate the lease", reason)
	}
}

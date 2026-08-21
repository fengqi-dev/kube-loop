package streamlease

import (
	"bytes"
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

func TestLeaseUsesEarliestAccessAndSessionExpiry(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{
			Subject: identityID, DeviceID: "device", AccessExpiresAt: now.Add(30 * time.Millisecond),
		},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)},
		Config{
			Now: func() time.Time { return now }, CheckInterval: 20 * time.Millisecond,
		},
	)
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
	if _, _, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{},
		sessionapi.ActiveSession{ExpiresAt: now},
		Config{Now: func() time.Time { return now }},
	); err == nil {
		t.Fatal("expired Session produced a lease")
	}
}

func TestLeaseFollowsHeartbeatExtendedSessionExpiry(t *testing.T) {
	stateStore, identityID, sessionID, _ := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	stored, err := stateStore.Sessions().
		GetByID(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	initialNow := time.Now().UTC()
	var clock atomic.Int64
	clock.Store(initialNow.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	initialExpiry := initialNow.Add(time.Minute)
	if err := stateStore.Sessions().Heartbeat(
		context.Background(),
		sessionID, stored.Generation, stored.NetworkSpec, stored.NetworkSpecHash, initialNow, initialExpiry,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{
			Subject: identityID, DeviceID: "device",
		},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: initialExpiry},
		Config{
			Now: now, CheckInterval: 10 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	heartbeatAt := initialNow.Add(time.Second)
	if err := stateStore.Sessions().Heartbeat(
		context.Background(), sessionID, stored.Generation+1, stored.NetworkSpec, stored.NetworkSpecHash,
		heartbeatAt, heartbeatAt.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	clock.Store(initialExpiry.Add(time.Second).UnixNano())
	select {
	case <-ctx.Done():
		t.Fatalf(
			"heartbeat-extended Session terminated its lease: %v",
			ctx.Err(),
		)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestLeaseTerminatesAfterOAuthGrantRevocation(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	authorizationID := uuid.NewString()
	if err := stateStore.OAuthSessions().Create(context.Background(), storage.OAuthSession{
		Kind:          "refresh_token",
		SignatureHash: bytes.Repeat([]byte{7}, 32),
		RequestID:     authorizationID,
		IdentityID:    identityID,
		ClientID:      "desktop",
		DeviceID:      "device",
		RequestJSON:   []byte(`{}`),
		Status:        statusActive,
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{
			Subject: identityID, DeviceID: "device", AuthorizationID: authorizationID,
		},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)},
		Config{CheckInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := stateStore.OAuthSessions().
		RevokeRequest(context.Background(), authorizationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(ctx, t, "revoked OAuth grant")
}

func TestFamilyBackedLeaseOutlivesOpeningAccessToken(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	authorizationID := uuid.NewString()
	if err := stateStore.OAuthSessions().Create(context.Background(), storage.OAuthSession{
		Kind:          "refresh_token",
		SignatureHash: bytes.Repeat([]byte{8}, 32),
		RequestID:     authorizationID,
		IdentityID:    identityID,
		ClientID:      "desktop",
		DeviceID:      "device",
		RequestJSON:   []byte(`{}`),
		Status:        statusActive,
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{
			Subject:         identityID,
			DeviceID:        "device",
			AuthorizationID: authorizationID,
			AccessExpiresAt: time.Now().Add(30 * time.Millisecond),
		},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)},
		Config{CheckInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf(
			"opening access-token expiry terminated a Family-backed lease: %v",
			ctx.Err(),
		)
	case <-time.After(60 * time.Millisecond):
	}
	if err := stateStore.OAuthSessions().
		RevokeRequest(context.Background(), authorizationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(
		ctx,
		t,
		"revoked OAuth grant after access-token refresh",
	)
}

func TestLeaseTerminatesAfterSessionStops(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{
			Subject: identityID, DeviceID: "device",
		},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)},
		Config{CheckInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := stateStore.Sessions().
		UpdateState(context.Background(), sessionID, 1, "stopped", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(ctx, t, "stopped Session")
}

func createLeaseStore(
	t *testing.T,
) (*storage.Store, string, string, time.Time) {
	t.Helper()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "lease.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	identityID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Identities().Create(context.Background(), storage.Identity{
		ID:          identityID,
		Type:        "human",
		DisplayName: "Test Identity",
		Status:      statusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(
		networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}},
	)
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: statusActive, Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore, identityID, sessionID, now
}

func waitForCancellation(ctx context.Context, t *testing.T, reason string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("%s did not terminate the lease", reason)
	}
}

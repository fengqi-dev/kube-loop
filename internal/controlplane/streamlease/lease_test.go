package streamlease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type denyingAuthorizer struct{}

func (denyingAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	return authorization.Decision{}
}

type countingDenyingAuthorizer struct{ calls *atomic.Int64 }

func (authorizer countingDenyingAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	authorizer.calls.Add(1)
	return authorization.Decision{}
}

type emptyStore struct{}

func (emptyStore) OAuthSessions() storage.OAuthSessionRepository { return nil }
func (emptyStore) Sessions() storage.SessionRepository           { return nil }
func (emptyStore) Tasks() storage.TaskRepository                 { return nil }

type blockingRuntime struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (runtime *blockingRuntime) AttachRuntime(
	context.Context,
	string,
	string,
) (context.Context, func(), error) {
	close(runtime.entered)
	<-runtime.release
	return nil, nil, runtime.err
}

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

func TestLeaseStartsWatcherOnlyAfterRuntimeAttach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = 10 * time.Millisecond
		now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
		attachErr := errors.New("attach failed")
		runtime := &blockingRuntime{
			entered: make(chan struct{}),
			release: make(chan struct{}),
			err:     attachErr,
		}
		released := false
		defer func() {
			if !released {
				close(runtime.release)
			}
		}()
		var authorizationCalls atomic.Int64
		result := make(chan error, 1)
		go func() {
			_, _, err := Start(
				t.Context(),
				emptyStore{},
				controlplaneapi.Identity{Subject: "identity", DeviceID: "device"},
				sessionapi.ActiveSession{ID: "session", ExpiresAt: now.Add(time.Hour)},
				Config{
					Now:           func() time.Time { return now },
					CheckInterval: interval,
					Runtime:       runtime,
					Authorizer: countingDenyingAuthorizer{
						calls: &authorizationCalls,
					},
				},
			)
			result <- err
		}()

		synctest.Wait()
		select {
		case <-runtime.entered:
		default:
			t.Fatal("runtime attach did not start")
		}
		time.Sleep(interval)
		synctest.Wait()
		if got := authorizationCalls.Load(); got != 0 {
			t.Fatalf("authorization calls before runtime attach = %d, want 0", got)
		}

		close(runtime.release)
		released = true
		synctest.Wait()
		select {
		case err := <-result:
			if !errors.Is(err, attachErr) {
				t.Fatalf("Start error = %v, want %v", err, attachErr)
			}
		default:
			t.Fatal("Start did not return after runtime attach failed")
		}
	})
}

func TestWatcherRetriesSingleValidationError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Second
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var calls atomic.Int64
		go watchValidation(ctx, cancel, interval, func(context.Context) (bool, error) {
			if calls.Add(1) == 1 {
				return false, errors.New("temporary storage failure")
			}
			return true, nil
		})

		time.Sleep(interval)
		synctest.Wait()
		if err := ctx.Err(); err != nil {
			t.Fatalf("single validation error canceled lease: %v", err)
		}
		time.Sleep(interval)
		synctest.Wait()
		if err := ctx.Err(); err != nil {
			t.Fatalf("successful retry did not preserve lease: %v", err)
		}
	})
}

func TestWatcherCancelsAfterRepeatedValidationErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Second
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go watchValidation(ctx, cancel, interval, func(context.Context) (bool, error) {
			return false, errors.New("persistent storage failure")
		})

		time.Sleep(interval)
		synctest.Wait()
		if err := ctx.Err(); err != nil {
			t.Fatalf("first validation error canceled lease: %v", err)
		}
		time.Sleep(interval)
		synctest.Wait()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("repeated validation errors left lease active: %v", ctx.Err())
		}
	})
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

func TestLeaseTerminatesAfterAuthorizationIsDenied(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{
			Subject: identityID, DeviceID: "device", Groups: []string{"developers"},
		},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)},
		Config{
			CheckInterval: 10 * time.Millisecond,
			Authorizer:    denyingAuthorizer{},
			Authorization: authorization.Request{
				Operation: "pods.exec", Namespace: "development",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	waitForCancellation(ctx, t, "denied authorization")
}

func TestFamilyBackedLeaseStillRequiresAuthorization(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	authorizationID := uuid.NewString()
	if err := stateStore.OAuthSessions().Create(context.Background(), storage.OAuthSession{
		Kind:          "refresh_token",
		SignatureHash: bytes.Repeat([]byte{9}, 32),
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
		Config{
			CheckInterval: 10 * time.Millisecond,
			Authorizer:    denyingAuthorizer{},
			Authorization: authorization.Request{
				Operation: "pods.exec", Namespace: "development",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	waitForCancellation(ctx, t, "denied Family-backed authorization")
}

func TestLeaseHeartbeatsOwnedTaskAndStopsWithOwnership(t *testing.T) {
	stateStore, identityID, sessionID, now := createLeaseStore(t)
	defer func() { _ = stateStore.Close() }()
	task := storage.Task{
		ID:             uuid.NewString(),
		IdentityID:     identityID,
		SessionID:      sessionID,
		Type:           "pod-exec",
		State:          remotetask.Running,
		Spec:           json.RawMessage(`{}`),
		Result:         json.RawMessage(`{}`),
		IdempotencyKey: uuid.NewString(),
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
	}
	if err := stateStore.Tasks().Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := Start(
		context.Background(),
		stateStore,
		controlplaneapi.Identity{Subject: identityID, DeviceID: "device"},
		sessionapi.ActiveSession{ID: sessionID, ExpiresAt: now.Add(time.Hour)},
		Config{
			CheckInterval: 10 * time.Millisecond,
			HeartbeatTask: true,
			TaskID:        task.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	heartbeat := waitForTaskUpdate(t, stateStore, task.ID, task.UpdatedAt)
	if err := stateStore.Tasks().UpdateState(
		context.Background(),
		task.ID,
		remotetask.Running,
		remotetask.Stopped,
		heartbeat.Result,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	waitForCancellation(ctx, t, "terminal Task ownership")
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

func waitForTaskUpdate(
	t *testing.T,
	stateStore *storage.Store,
	taskID string,
	previous time.Time,
) storage.Task {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		task, err := stateStore.Tasks().GetByID(context.Background(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.UpdatedAt.After(previous) {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Task owner heartbeat did not advance")
	return storage.Task{}
}

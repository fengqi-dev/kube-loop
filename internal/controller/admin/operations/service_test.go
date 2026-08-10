package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
)

type testRuntime struct {
	store       *storage.Store
	wantSession string
	calls       int
	err         error
}

func (runtime *testRuntime) Disconnect(ctx context.Context, sessionID string) error {
	runtime.calls++
	if sessionID != runtime.wantSession {
		return errors.New("unexpected Session runtime identity")
	}
	session, err := runtime.store.Sessions().GetByID(ctx, sessionID)
	if err != nil || session.State != "stopped" || session.Generation != 2 {
		return errors.New("Session state was not committed before runtime convergence")
	}
	return runtime.err
}

func TestStopSessionCommitsAuditBeforeRuntimeAndReplays(t *testing.T) {
	store, principal, session, now := newTestAggregate(t)
	runtime := &testRuntime{store: store, wantSession: session.ID}
	service, err := New(store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	key := "operation-key-00000001"
	request := StopSessionRequest{
		Request: Request{
			Actor:          Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal},
			IdempotencyKey: key, Reason: "security response", RequestID: uuid.NewString(),
		},
		SessionID: session.ID, ExpectedGeneration: 1,
	}
	result, err := service.StopSession(context.Background(), request)
	if err != nil || !result.RuntimeConverged || result.Generation != 2 || runtime.calls != 1 {
		t.Fatalf("StopSession = %#v, %v; calls=%d", result, err, runtime.calls)
	}
	replayed, err := service.StopSession(context.Background(), request)
	if err != nil || !replayed.Replayed || !replayed.RuntimeConverged || runtime.calls != 2 {
		t.Fatalf("replayed StopSession = %#v, %v; calls=%d", replayed, err, runtime.calls)
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Action: "admin.session.stop"})
	if err != nil || len(events) != 1 || strings.Contains(string(events[0].Metadata), key) {
		t.Fatalf("Session stop audit = %#v, %v", events, err)
	}
	digest := sha256.Sum256([]byte(key))
	scope := "admin-operation:normal:" + principal.ID + ":admin.session.stop"
	if _, err := store.Idempotency().Get(context.Background(), scope, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("plaintext idempotency key was persisted: %v", err)
	}
	if _, err := store.Idempotency().Get(context.Background(), scope, hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("hashed idempotency record missing: %v", err)
	}
}

func TestRevocationsAreOwnerSafeAndAtomicWithAudit(t *testing.T) {
	store, principal, _, now := newTestAggregate(t)
	runtime := &testRuntime{store: store}
	service, err := New(store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	family := storage.TokenFamily{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "laptop",
		RefreshTokenHash: bytes.Repeat([]byte{8}, 32), CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	if err := store.TokenFamilies().Create(context.Background(), family); err != nil {
		t.Fatal(err)
	}
	base := Request{
		Actor:          Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal},
		IdempotencyKey: "operation-key-00000002", Reason: "revoke lost device", RequestID: uuid.NewString(),
	}
	_, err = service.RevokeDeviceSession(context.Background(), RevokeDeviceSessionRequest{
		Request: base, PrincipalID: uuid.NewString(), DeviceSessionID: family.ID,
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("cross-Principal revoke error = %v", err)
	}
	loaded, _ := store.TokenFamilies().GetByID(context.Background(), family.ID)
	if loaded.RevokedAt != nil {
		t.Fatal("cross-Principal revoke changed the Device Session")
	}
	base.IdempotencyKey = "operation-key-00000003"
	result, err := service.RevokePrincipal(context.Background(), RevokePrincipalRequest{
		Request: base, PrincipalID: principal.ID,
	})
	if err != nil || result.RevokedCount != 1 {
		t.Fatalf("RevokePrincipal = %#v, %v", result, err)
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Action: "admin.principal.revoke"})
	if err != nil || len(events) != 1 {
		t.Fatalf("Principal revoke audit = %#v, %v", events, err)
	}
}

func TestStopSessionReportsPendingRuntimeConvergence(t *testing.T) {
	store, principal, session, now := newTestAggregate(t)
	runtime := &testRuntime{store: store, wantSession: session.ID, err: errors.New("runtime timeout")}
	service, _ := New(store, runtime)
	service.now = func() time.Time { return now }
	result, err := service.StopSession(context.Background(), StopSessionRequest{
		Request: Request{
			Actor:          Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal},
			IdempotencyKey: "operation-key-00000004", Reason: "force stop incident", RequestID: uuid.NewString(),
		},
		SessionID: session.ID, ExpectedGeneration: 1,
	})
	if err != nil || result.RuntimeConverged {
		t.Fatalf("StopSession convergence result = %#v, %v", result, err)
	}
	stored, _ := store.Sessions().GetByID(context.Background(), session.ID)
	if stored.State != "stopped" || stored.Generation != 2 {
		t.Fatalf("durable Session state = %#v", stored)
	}
}

func TestStopTaskUsesObservedVersionAndDurableTransition(t *testing.T) {
	store, principal, session, now := newTestAggregate(t)
	service, _ := New(store, &testRuntime{store: store})
	service.now = func() time.Time { return now }
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.ID, SessionID: session.ID, Type: "preview",
		State: remotetask.Running, Spec: []byte(`{"namespace":"default"}`), IdempotencyKey: "task-create-00000001",
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	if err := store.Tasks().Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	request := StopTaskRequest{
		Request: Request{
			Actor:          Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal},
			IdempotencyKey: "operation-key-00000005", Reason: "stop stale preview", RequestID: uuid.NewString(),
		},
		TaskID: task.ID, ExpectedVersion: taskVersion(task.UpdatedAt),
	}
	result, err := service.StopTask(context.Background(), request)
	if err != nil || result.State != string(remotetask.Stopping) || !result.PendingConvergence || result.Version != taskVersion(now) {
		t.Fatalf("StopTask = %#v, %v", result, err)
	}
	stored, err := store.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != remotetask.Stopping || !bytes.Contains(stored.Result, []byte(`"source":"management"`)) {
		t.Fatalf("stored Task = %#v, %v", stored, err)
	}
	request.IdempotencyKey = "operation-key-00000006"
	request.ExpectedVersion = taskVersion(task.UpdatedAt)
	if _, err := service.StopTask(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Task version error = %v", err)
	}
}

func newTestAggregate(t *testing.T) (*storage.Store, storage.Principal, storage.Session, time.Time) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "operations.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: "operator", CreatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		t.Fatal(err)
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	session := storage.Session{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "workstation", ClusterID: "cluster-a",
		Namespace: "default", State: "active", Generation: 1, NetworkSpec: specJSON, NetworkSpecHash: specHash,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute), LastHeartbeatAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return store, principal, session, now
}

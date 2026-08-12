package sessionregistry

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestReconcilerTerminatesOnlyStaleStreamOwners(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)
	store, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "runtime-recovery.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := store.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "runtime-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}})
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	if err := store.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	tasks := map[string]storage.Task{
		"stale-exec": {
			ID: uuid.NewString(), PrincipalID: principalID, SessionID: sessionID, Type: "pod-exec",
			State: remotetask.Running, Spec: json.RawMessage(`{}`), Result: json.RawMessage(`{"exitCode":0}`),
			IdempotencyKey: "stale-exec", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
		},
		"stopping-file": {
			ID: uuid.NewString(), PrincipalID: principalID, SessionID: sessionID, Type: "file-transfer",
			State: remotetask.Stopping, Spec: json.RawMessage(`{}`), Result: json.RawMessage(`{"bytes":10}`),
			IdempotencyKey: "stopping-file", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
		},
		"live-exec": {
			ID: uuid.NewString(), PrincipalID: principalID, SessionID: sessionID, Type: "pod-exec",
			State: remotetask.Running, Spec: json.RawMessage(`{}`), Result: json.RawMessage(`{}`),
			IdempotencyKey: "live-exec", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Second),
		},
		"live-port-forward": {
			ID: uuid.NewString(), PrincipalID: principalID, SessionID: sessionID, Type: "port-forward",
			State: remotetask.Running, Spec: json.RawMessage(`{}`), Result: json.RawMessage(`{}`),
			IdempotencyKey: "live-port-forward", CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
		},
	}
	for _, task := range tasks {
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	reconciler, err := NewReconciler(
		store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecoveryConfig{Interval: time.Second, StaleAfter: 10 * time.Second, Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := reconciler.RunOnce(ctx); err != nil || count != 2 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	want := map[string]remotetask.State{
		"stale-exec": remotetask.Failed, "stopping-file": remotetask.Stopped,
		"live-exec": remotetask.Running, "live-port-forward": remotetask.Running,
	}
	for name, task := range tasks {
		loaded, err := store.Tasks().GetByID(ctx, task.ID)
		if err != nil || loaded.State != want[name] {
			t.Fatalf("%s = %#v, %v; want %s", name, loaded, err, want[name])
		}
	}
	if err := store.Sessions().UpdateState(ctx, sessionID, 1, "disconnected", now); err != nil {
		t.Fatal(err)
	}
	if count, err := reconciler.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("disconnected RunOnce() = %d, %v", count, err)
	}
	portForward, err := store.Tasks().GetByID(ctx, tasks["live-port-forward"].ID)
	if err != nil || portForward.State != remotetask.Stopped {
		t.Fatalf("disconnected Port Forward = %#v, %v", portForward, err)
	}
}

package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestTaskTransitionRollsBackWhenAuditAppendFails(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "task-audit-rollback.db"))
	ctx := context.Background()
	principal := createTestPrincipal(t, store.Principals(), "task-audit-rollback")
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := networkspec.CanonicalJSON(spec)
	specHash, _ := networkspec.Hash(spec)
	session := Session{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "audit-device", ClusterID: "cluster-a",
		Namespace: "development", State: "active", NetworkSpec: specJSON, NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	task := Task{
		ID: uuid.NewString(), PrincipalID: principal.ID, SessionID: session.ID,
		Type: "pod-exec", State: remotetask.Pending, Spec: json.RawMessage(`{"pod":"api"}`),
		IdempotencyKey: "task-audit-rollback", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Tasks().Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE audit_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.Tasks().UpdateState(
		ctx, task.ID, remotetask.Pending, remotetask.Running, json.RawMessage(`{"secret":"must-not-commit"}`), now.Add(time.Second),
	); err == nil {
		t.Fatal("Task transition succeeded without its audit event")
	}
	stored, err := store.Tasks().GetByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != remotetask.Pending || len(stored.Result) != 0 {
		t.Fatalf("Task transition was not rolled back: %#v", stored)
	}
}

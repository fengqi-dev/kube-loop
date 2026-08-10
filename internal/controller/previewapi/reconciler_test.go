package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
)

func TestPreviewReconcilerClaimsStaleOwnerOnceAndDeletes(t *testing.T) {
	stateStore, principal, active := previewTestStore(t)
	now := time.Now().UTC()
	task := stalePreviewTask(t, stateStore, principal.Subject, active.ID, now.Add(-time.Minute))
	resources := &recordingPreviewResources{}
	newWorker := func(owner string) *Reconciler {
		worker, err := NewReconciler(
			stateStore, resources, slog.New(slog.NewTextHandler(io.Discard, nil)),
			RecoveryConfig{
				OwnerID: owner, GatewayIP: "127.0.0.1", Interval: 100 * time.Millisecond,
				StaleAfter: time.Second, DeleteTimeout: time.Second, Now: func() time.Time { return now },
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return worker
	}
	workers := []*Reconciler{newWorker("controller-a"), newWorker("controller-b")}
	counts := make(chan int, len(workers))
	errorsChannel := make(chan error, len(workers))
	var group sync.WaitGroup
	for _, worker := range workers {
		group.Go(func() {
			count, err := worker.RunOnce(context.Background())
			counts <- count
			errorsChannel <- err
		})
	}
	group.Wait()
	close(counts)
	close(errorsChannel)
	total := 0
	for count := range counts {
		total += count
	}
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 1 || resources.deletes() != 1 {
		t.Fatalf("Preview recovery claims=%d deletes=%d", total, resources.deletes())
	}
	stored, err := stateStore.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != "failed" {
		t.Fatalf("recovered Preview Task=%#v err=%v", stored, err)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("recovered Preview cleanup intents=%#v err=%v", snapshots, err)
	}
}

func TestPreviewReconcilerRetainsIntentAndRetriesDelete(t *testing.T) {
	stateStore, principal, active := previewTestStore(t)
	clock := time.Now().UTC()
	task := stalePreviewTask(t, stateStore, principal.Subject, active.ID, clock.Add(-time.Minute))
	resources := &recordingPreviewResources{deleteErr: errors.New("Kubernetes unavailable")}
	worker, err := NewReconciler(stateStore, resources, nil, RecoveryConfig{
		OwnerID: "controller-recovery", GatewayIP: "127.0.0.1",
		Interval: 100 * time.Millisecond, StaleAfter: time.Second, DeleteTimeout: time.Second,
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := worker.RunOnce(context.Background()); count != 1 || err == nil {
		t.Fatalf("first Preview recovery count=%d err=%v", count, err)
	}
	stored, err := stateStore.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != "recovering" {
		t.Fatalf("deferred Preview recovery Task=%#v err=%v", stored, err)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("deferred Preview cleanup intents=%#v err=%v", snapshots, err)
	}
	resources.mu.Lock()
	resources.deleteErr = nil
	resources.mu.Unlock()
	clock = clock.Add(2 * time.Second)
	if count, err := worker.RunOnce(context.Background()); count != 1 || err != nil {
		t.Fatalf("retry Preview recovery count=%d err=%v", count, err)
	}
	stored, err = stateStore.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != "failed" || resources.deletes() != 2 {
		t.Fatalf("retried Preview Task=%#v deletes=%d err=%v", stored, resources.deletes(), err)
	}
}

func stalePreviewTask(
	t *testing.T,
	stateStore *storage.Store,
	principalID, sessionID string,
	updatedAt time.Time,
) storage.Task {
	t.Helper()
	spec, _ := json.Marshal(storedSpec{
		Name: "local-api", Ports: []Port{{Name: "http", ServicePort: 80, Protocol: "tcp"}},
	})
	result, _ := json.Marshal(ownerResult{ClusterIP: "10.96.0.40"})
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principalID, SessionID: sessionID,
		Type: TaskType, State: "running", Spec: spec, Result: result, IdempotencyKey: uuid.NewString(),
		CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt,
	}
	if err := stateStore.Tasks().Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(servicebinding.PreviewServiceSnapshot{
		Namespace: "development", Service: "local-api", GatewayIP: "127.0.0.1",
		Ports: []servicebinding.InterceptPort{{
			Name: "http", ServicePort: 80, ListenPort: 30000, Protocol: corev1.ProtocolTCP,
		}},
	})
	if err := stateStore.ResourceSnapshots().Put(context.Background(), storage.ResourceSnapshot{
		ID: uuid.NewString(), TaskID: task.ID, Kind: previewSnapshotKind,
		Namespace: "development", Name: "local-api", Data: snapshot, CreatedAt: updatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	return task
}

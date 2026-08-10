package exchangeapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

type recoveryResources struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (*recoveryResources) Capture(context.Context, controller.Principal, *servicebinding.ServiceInterceptSnapshot) error {
	return nil
}

func (*recoveryResources) Apply(context.Context, controller.Principal, servicebinding.ServiceInterceptSnapshot, string) error {
	return nil
}

func (resources *recoveryResources) Restore(context.Context, servicebinding.ServiceInterceptSnapshot, string) error {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.calls++
	return resources.err
}

func (resources *recoveryResources) count() int {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return resources.calls
}

func TestExchangeReconcilerClaimsStaleOwnerOnceAndRestores(t *testing.T) {
	stateStore, principal, active := exchangeStreamStore(t)
	now := time.Now().UTC()
	task := staleExchangeTask(t, stateStore, principal.Subject, active.ID, now.Add(-time.Minute), true)
	resources := &recoveryResources{}
	newWorker := func(owner string) *Reconciler {
		worker, err := NewReconciler(stateStore, resources, slog.New(slog.NewTextHandler(io.Discard, nil)), RecoveryConfig{
			OwnerID: owner, GatewayIP: "127.0.0.1", Interval: 100 * time.Millisecond,
			StaleAfter: time.Second, RestoreTimeout: time.Second, Now: func() time.Time { return now },
		})
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
	if total != 1 || resources.count() != 1 {
		t.Fatalf("recovery claims=%d restores=%d", total, resources.count())
	}
	stored, err := stateStore.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != "failed" {
		t.Fatalf("recovered Task=%#v err=%v", stored, err)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("recovered snapshots=%#v err=%v", snapshots, err)
	}
}

func TestExchangeReconcilerRetainsSnapshotAndRetriesRestore(t *testing.T) {
	stateStore, principal, active := exchangeStreamStore(t)
	clock := time.Now().UTC()
	task := staleExchangeTask(t, stateStore, principal.Subject, active.ID, clock.Add(-time.Minute), true)
	resources := &recoveryResources{err: context.DeadlineExceeded}
	worker, err := NewReconciler(stateStore, resources, nil, RecoveryConfig{
		OwnerID: "controller-recovery", GatewayIP: "127.0.0.1",
		Interval: 100 * time.Millisecond, StaleAfter: time.Second, RestoreTimeout: time.Second,
		Now: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := worker.RunOnce(context.Background()); count != 1 || err == nil {
		t.Fatalf("first recovery count=%d err=%v", count, err)
	}
	stored, err := stateStore.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != "recovering" {
		t.Fatalf("deferred recovery Task=%#v err=%v", stored, err)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("deferred recovery snapshots=%#v err=%v", snapshots, err)
	}
	resources.mu.Lock()
	resources.err = nil
	resources.mu.Unlock()
	clock = clock.Add(2 * time.Second)
	if count, err := worker.RunOnce(context.Background()); count != 1 || err != nil {
		t.Fatalf("retry recovery count=%d err=%v", count, err)
	}
	stored, err = stateStore.Tasks().GetByID(context.Background(), task.ID)
	if err != nil || stored.State != "failed" || resources.count() != 2 {
		t.Fatalf("retried recovery Task=%#v restores=%d err=%v", stored, resources.count(), err)
	}
}

func staleExchangeTask(
	t *testing.T,
	stateStore *storage.Store,
	principalID, sessionID string,
	updatedAt time.Time,
	withSnapshot bool,
) storage.Task {
	t.Helper()
	spec, _ := json.Marshal(storedSpec{
		Service: "api", ClusterIP: "10.96.0.20", Ports: []Port{{ServicePort: 80, Protocol: "tcp"}},
	})
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principalID, SessionID: sessionID,
		Type: TaskType, State: "running", Spec: spec, IdempotencyKey: uuid.NewString(),
		CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt,
	}
	if err := stateStore.Tasks().Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if withSnapshot {
		snapshot, _ := json.Marshal(servicebinding.ServiceInterceptSnapshot{
			Namespace: "development", Service: "api", GatewayIP: "127.0.0.1",
			Ports: []servicebinding.InterceptPort{{ServicePort: 80, ListenPort: 30000, Protocol: "TCP"}},
		})
		if err := stateStore.ResourceSnapshots().Put(context.Background(), storage.ResourceSnapshot{
			ID: uuid.NewString(), TaskID: task.ID, Kind: exchangeSnapshotKind,
			Namespace: "development", Name: "api", Data: snapshot, CreatedAt: updatedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

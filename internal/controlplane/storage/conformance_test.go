package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestSQLiteRepositoryConformance(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "conformance.db"))
	testRepositoryConformance(t, store)
}

func TestPostgreSQLRepositoryConformance(t *testing.T) {
	config, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testRepositoryConformance(t, store)
}

func testRepositoryConformance(t *testing.T, store *Store) {
	t.Helper()
	t.Run("sessions tasks and snapshots", func(t *testing.T) { testSessionTaskSnapshotRepositories(t, store) })
	t.Run("idempotency", func(t *testing.T) { testIdempotencyRepository(t, store) })
	t.Run("audit", func(t *testing.T) { testAuditRepository(t, store) })
	t.Run("Relay desired states", func(t *testing.T) { testRelayDesiredStateRepository(t, store) })
	t.Run("audit export jobs", func(t *testing.T) { testAuditExportJobRepository(t, store) })
	t.Run("management list pagination", func(t *testing.T) { testManagementListPagination(t, store) })
	t.Run("management sessions", func(t *testing.T) { testAdminSessionRepositoryConformance(t, store) })
	t.Run("transactions", func(t *testing.T) { testTransactions(t, store) })
	t.Run("concurrent identity and idempotency", func(t *testing.T) { testConcurrentIdentityAndIdempotency(t, store) })
	t.Run("stable errors", func(t *testing.T) { testStableRepositoryErrors(t, store) })
}

func testRelayDesiredStateRepository(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 12, 30, 0, 0, time.UTC)
	relayID := "relay-conformance-a"
	created, err := store.RelayDesiredStates().CompareAndSwap(
		ctx, relayID, "draining", 0, ManagementActorBreakGlass, "break-glass", "drain for maintenance", now,
	)
	if err != nil || created.Version != 1 || created.DesiredState != "draining" {
		t.Fatalf("create Relay desired state = %#v, %v", created, err)
	}
	if _, err := store.RelayDesiredStates().CompareAndSwap(
		ctx, relayID, "ready", 0, ManagementActorBreakGlass, "break-glass", "stale recovery", now,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Relay desired state error = %v", err)
	}
	updated, err := store.RelayDesiredStates().CompareAndSwap(
		ctx, relayID, "ready", 1, ManagementActorBreakGlass, "break-glass", "restore capacity", now.Add(time.Minute),
	)
	if err != nil || updated.Version != 2 || updated.DesiredState != "ready" {
		t.Fatalf("update Relay desired state = %#v, %v", updated, err)
	}
	listed, err := store.RelayDesiredStates().List(ctx)
	if err != nil || len(listed) != 1 || listed[0].Version != 2 {
		t.Fatalf("list Relay desired states = %#v, %v", listed, err)
	}
}

func testAuditExportJobRepository(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	job := AuditExportJob{
		ID: uuid.NewString(), State: "pending", Filter: json.RawMessage(`{"limit":10}`),
		RequestedBy: ManagementActorBreakGlass, RequestedAuthenticationType: "break-glass", Reason: "export conformance audit",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.AuditExportJobs().Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	pending, err := store.AuditExportJobs().ListRunnable(ctx, now.Add(-time.Minute), 10)
	if err != nil || len(pending) != 1 || pending[0].ID != job.ID {
		t.Fatalf("pending audit export jobs = %#v, %v", pending, err)
	}
	claimedAt := now.Add(time.Minute)
	if err := store.AuditExportJobs().Claim(ctx, job.ID, now, now.Add(-time.Minute), claimedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.AuditExportJobs().Claim(ctx, job.ID, now, now.Add(-time.Minute), claimedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate audit export claim error = %v", err)
	}
	if err := store.AuditExportJobs().Complete(ctx, job.ID, "succeeded", "{}\n", "", claimedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.AuditExportJobs().GetByID(ctx, job.ID)
	if err != nil || loaded.State != "succeeded" || loaded.Result != "{}\n" {
		t.Fatalf("completed audit export job = %#v, %v", loaded, err)
	}
}

func testManagementListPagination(t *testing.T, store *Store) {
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	search := "pagination-" + uuid.NewString()
	identities := make([]Identity, 0, 3)
	for index := range 3 {
		identity, err := store.Identities().Create(ctx, Identity{
			ID: uuid.NewString(), Type: "human", DisplayName: fmt.Sprintf("%s-user-%d", search, index), Status: "active",
			CreatedAt: now.Add(time.Duration(index) * time.Second), UpdatedAt: now.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, identity)
	}
	firstIdentities, err := store.Identities().List(ctx, IdentityListFilter{Search: search, Limit: 2})
	if err != nil || len(firstIdentities) != 2 || firstIdentities[0].ID != identities[2].ID || firstIdentities[1].ID != identities[1].ID {
		t.Fatalf("first identity page=%#v error=%v", firstIdentities, err)
	}
	if _, err := store.Identities().Create(ctx, Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: search + "-inserted-after-page-one", Status: "active",
		CreatedAt: now.Add(10 * time.Second), UpdatedAt: now.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	secondIdentities, err := store.Identities().List(ctx, IdentityListFilter{
		Search: search, Limit: 2, Cursor: pageCursor(firstIdentities[1].CreatedAt, firstIdentities[1].ID),
	})
	if err != nil || len(secondIdentities) != 1 || secondIdentities[0].ID != identities[0].ID {
		t.Fatalf("second identity page=%#v error=%v", secondIdentities, err)
	}

	sessions := make([]Session, 0, 3)
	for index := range 3 {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		session := Session{
			ID: uuid.NewString(), IdentityID: identities[0].ID, DeviceID: fmt.Sprintf("device-%d", index),
			ClusterID: "cluster-pagination", Namespace: "pagination", State: "active",
			CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Hour),
		}
		if err := store.Sessions().Create(ctx, session); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	firstSessions, err := store.Sessions().List(ctx, SessionListFilter{Namespace: "pagination", State: "active", Limit: 2})
	if err != nil || len(firstSessions) != 2 || firstSessions[0].ID != sessions[2].ID {
		t.Fatalf("first session page=%#v error=%v", firstSessions, err)
	}
	secondSessions, err := store.Sessions().List(ctx, SessionListFilter{
		Namespace: "pagination", Limit: 2, Cursor: pageCursor(firstSessions[1].CreatedAt, firstSessions[1].ID),
	})
	if err != nil || len(secondSessions) != 1 || secondSessions[0].ID != sessions[0].ID {
		t.Fatalf("second session page=%#v error=%v", secondSessions, err)
	}

	tasks := make([]Task, 0, 3)
	for index := range 3 {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		task := Task{
			ID: uuid.NewString(), IdentityID: identities[0].ID, SessionID: sessions[index].ID,
			Type: "pagination-task", State: remotetask.Pending, Spec: json.RawMessage(`{"safe":true}`),
			IdempotencyKey: fmt.Sprintf("pagination-%d", index), CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		if err := store.Tasks().Create(ctx, task); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	firstTasks, err := store.Tasks().List(ctx, TaskListFilter{Namespace: "pagination", Type: "pagination-task", Limit: 2})
	if err != nil || len(firstTasks) != 2 || firstTasks[0].ID != tasks[2].ID {
		t.Fatalf("first task page=%#v error=%v", firstTasks, err)
	}
	secondTasks, err := store.Tasks().List(ctx, TaskListFilter{
		Namespace: "pagination", Limit: 2, Cursor: pageCursor(firstTasks[1].CreatedAt, firstTasks[1].ID),
	})
	if err != nil || len(secondTasks) != 1 || secondTasks[0].ID != tasks[0].ID {
		t.Fatalf("second task page=%#v error=%v", secondTasks, err)
	}

	action := "pagination.audit." + uuid.NewString()
	events := make([]AuditEvent, 0, 3)
	for index := range 3 {
		event := AuditEvent{
			ID: uuid.NewString(), IdentityID: identities[0].ID, Action: action, Outcome: "success",
			RequestID: fmt.Sprintf("pagination-request-%d", index), CreatedAt: now.Add(time.Duration(index) * time.Minute),
		}
		if err := store.Audit().Append(ctx, event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	firstEvents, err := store.Audit().List(ctx, AuditFilter{Action: action, Limit: 2})
	if err != nil || len(firstEvents) != 2 || firstEvents[0].ID != events[2].ID {
		t.Fatalf("first audit page=%#v error=%v", firstEvents, err)
	}
	secondEvents, err := store.Audit().List(ctx, AuditFilter{
		Action: action, Limit: 2, Cursor: pageCursor(firstEvents[1].CreatedAt, firstEvents[1].ID),
	})
	if err != nil || len(secondEvents) != 1 || secondEvents[0].ID != events[0].ID {
		t.Fatalf("second audit page=%#v error=%v", secondEvents, err)
	}
}

func testConcurrentIdentityAndIdempotency(t *testing.T, store *Store) {
	ctx := context.Background()
	const workers = 16
	identity := createTestIdentity(t, store.Identities(), "concurrent")

	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	record := IdempotencyRecord{
		Scope: "conformance:" + identity.ID, Key: "concurrent", RequestHash: "sha256:concurrent",
		ResourceType: "task", ResourceID: uuid.NewString(), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	var created atomic.Int32
	errorsCh := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			stored, wasCreated, err := store.Idempotency().Reserve(ctx, record)
			if err != nil {
				errorsCh <- err
				return
			}
			if stored.ResourceID != record.ResourceID {
				errorsCh <- errors.New("concurrent idempotency returned a different resource")
				return
			}
			if wasCreated {
				created.Add(1)
			}
		})
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if created.Load() != 1 {
		t.Fatalf("concurrent idempotency creators = %d", created.Load())
	}
}

func testStableRepositoryErrors(t *testing.T, store *Store) {
	ctx := context.Background()
	if _, err := store.Identities().GetByID(ctx, "not-a-uuid"); err == nil || err.Error() != "identity ID must be a UUID" {
		t.Fatalf("invalid ID error = %v", err)
	}
	if _, err := store.Identities().GetByID(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing object error = %v", err)
	}
	record := IdempotencyRecord{
		Scope: "stable-errors", Key: uuid.NewString(), RequestHash: "sha256:first",
		ResourceType: "task", ResourceID: uuid.NewString(),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if _, created, err := store.Idempotency().Reserve(ctx, record); err != nil || !created {
		t.Fatalf("initial stable-error reservation = %t, %v", created, err)
	}
	record.RequestHash = "sha256:second"
	if _, _, err := store.Idempotency().Reserve(ctx, record); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("idempotency mismatch error = %v", err)
	}
}

func testSessionTaskSnapshotRepositories(t *testing.T, store *Store) {
	ctx := context.Background()
	identity := createTestIdentity(t, store.Identities(), "session-user")
	now := time.Date(2026, 8, 9, 3, 0, 0, 100, time.UTC)
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := networkspec.CanonicalJSON(spec)
	specHash, _ := networkspec.Hash(spec)
	session := Session{
		ID: uuid.NewString(), IdentityID: identity.ID, DeviceID: "device-2", ClusterID: "cluster-a",
		Namespace: "development", State: "starting", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		NetworkSpec: specJSON, NetworkSpecHash: specHash,
	}
	if err := store.Sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().UpdateState(ctx, session.ID, 1, "active", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().UpdateState(ctx, session.ID, 1, "stopped", now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale session generation error = %v", err)
	}
	loadedSession, err := store.Sessions().GetByID(ctx, session.ID)
	if err != nil || loadedSession.Generation != 2 || loadedSession.State != "active" ||
		loadedSession.NetworkSpecHash != specHash || !bytes.Equal(loadedSession.NetworkSpec, specJSON) {
		t.Fatalf("loaded session = %#v, %v", loadedSession, err)
	}
	if err := store.Sessions().Heartbeat(
		ctx, session.ID, 2, specJSON, specHash, now.Add(2*time.Second), now.Add(10*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	loadedSession, err = store.Sessions().GetByID(ctx, session.ID)
	if err != nil || loadedSession.Generation != 3 || !loadedSession.LastHeartbeatAt.Equal(now.Add(2*time.Second)) ||
		!loadedSession.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("heartbeat session = %#v, %v", loadedSession, err)
	}
	task := Task{
		ID: uuid.NewString(), IdentityID: identity.ID, SessionID: session.ID,
		Type: "port-forward", State: "pending", Spec: json.RawMessage(`{"port":8080}`),
		IdempotencyKey: "task-key", CreatedAt: now,
	}
	if err := store.Tasks().Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	duplicate := task
	duplicate.ID = uuid.NewString()
	if err := store.Tasks().Create(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate task idempotency key error = %v", err)
	}
	apiContext := WithAuditRequestID(ctx, "request-task-running")
	if err := store.Tasks().UpdateState(apiContext, task.ID, "pending", "running", json.RawMessage(`{"localPort":18080}`), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.Tasks().UpdateState(ctx, task.ID, "pending", "failed", nil, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale task state error = %v", err)
	}
	loadedTask, err := store.Tasks().GetByID(ctx, task.ID)
	if err != nil || loadedTask.State != "running" || !json.Valid(loadedTask.Result) {
		t.Fatalf("loaded task = %#v, %v", loadedTask, err)
	}
	tasks, err := store.Tasks().ListBySession(ctx, session.ID, 10)
	if err != nil || len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("session tasks = %#v, %v", tasks, err)
	}
	stale, err := store.Tasks().ListStaleByTypeStates(ctx, "port-forward", []remotetask.State{remotetask.Running}, now.Add(2*time.Second), 10)
	if err != nil || len(stale) != 1 || stale[0].ID != task.ID {
		t.Fatalf("stale tasks = %#v, %v", stale, err)
	}
	if err := store.Tasks().ClaimStale(
		ctx, task.ID, "running", loadedTask.UpdatedAt, "recovering", json.RawMessage(`{"owner":"worker-a"}`), now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Tasks().ClaimStale(
		ctx, task.ID, "running", loadedTask.UpdatedAt, "recovering", nil, now.Add(4*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate stale Task claim error = %v", err)
	}
	if err := store.Tasks().UpdateState(
		ctx, task.ID, remotetask.Recovering, remotetask.Failed, nil, now.Add(5*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	transitions, err := store.Audit().List(ctx, AuditFilter{Action: TaskTransitionAuditAction, Limit: 10})
	if err != nil || len(transitions) != 3 {
		t.Fatalf("Task transition audit events = %#v, %v", transitions, err)
	}
	wantStates := [][2]remotetask.State{
		{remotetask.Recovering, remotetask.Failed},
		{remotetask.Running, remotetask.Recovering},
		{remotetask.Pending, remotetask.Running},
	}
	for index, event := range transitions {
		if event.IdentityID != identity.ID || event.ResourceType != "port-forward" ||
			event.ResourceID != task.ID || event.Outcome != "success" {
			t.Fatalf("Task transition audit event = %#v", event)
		}
		var metadata struct {
			SessionID     string           `json:"sessionId"`
			Namespace     string           `json:"namespace"`
			PreviousState remotetask.State `json:"previousState"`
			NextState     remotetask.State `json:"nextState"`
			Source        string           `json:"source"`
		}
		if err := json.Unmarshal(event.Metadata, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.SessionID != session.ID || metadata.Namespace != session.Namespace ||
			metadata.PreviousState != wantStates[index][0] || metadata.NextState != wantStates[index][1] {
			t.Fatalf("Task transition audit metadata = %#v", metadata)
		}
		if strings.Contains(string(event.Metadata), "localPort") || strings.Contains(string(event.Metadata), "worker-a") {
			t.Fatalf("Task result leaked into audit metadata: %s", event.Metadata)
		}
		if index == 2 {
			if event.RequestID != "request-task-running" || metadata.Source != "api" {
				t.Fatalf("API Task transition correlation = %q, %#v", event.RequestID, metadata)
			}
		} else if !strings.HasPrefix(event.RequestID, "background-") || metadata.Source != "background" {
			t.Fatalf("background Task transition correlation = %q, %#v", event.RequestID, metadata)
		}
	}
	if err := store.Tasks().UpdateState(
		ctx, task.ID, remotetask.Failed, remotetask.Running, nil, now.Add(6*time.Second),
	); err == nil {
		t.Fatal("terminal Task was allowed to return to running")
	}
	legacy := task
	legacy.ID = uuid.NewString()
	legacy.State = remotetask.State("active")
	legacy.IdempotencyKey = "legacy-active-task"
	if err := store.Tasks().Create(ctx, legacy); err == nil {
		t.Fatal("Task with legacy active state was accepted")
	}
	snapshot := ResourceSnapshot{
		ID: uuid.NewString(), TaskID: task.ID, Kind: "Service", Namespace: "default", Name: "api",
		Data: json.RawMessage(`{"resourceVersion":"1"}`), CreatedAt: now,
	}
	if err := store.ResourceSnapshots().Put(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	replacement := snapshot
	replacement.ID = uuid.NewString()
	replacement.Data = json.RawMessage(`{"resourceVersion":"2"}`)
	if err := store.ResourceSnapshots().Put(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	snapshots, err := store.ResourceSnapshots().ListByTask(ctx, task.ID)
	if err != nil || len(snapshots) != 1 || !bytes.Contains(snapshots[0].Data, []byte(`"2"`)) {
		t.Fatalf("resource snapshots = %#v, %v", snapshots, err)
	}
	deleted, err := store.ResourceSnapshots().DeleteByTask(ctx, task.ID)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted snapshots = %d, %v", deleted, err)
	}

	expiredSession := Session{
		ID: uuid.NewString(), IdentityID: identity.ID, DeviceID: "expired", ClusterID: "cluster-a",
		Namespace: "development", State: "stopped", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	if err := store.Sessions().Create(ctx, expiredSession); err != nil {
		t.Fatal(err)
	}
	protectedTask := Task{
		ID: uuid.NewString(), IdentityID: identity.ID, SessionID: expiredSession.ID,
		Type: "exchange", State: "running", Spec: json.RawMessage(`{"service":"api"}`),
		IdempotencyKey: "protected-exchange", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.Tasks().Create(ctx, protectedTask); err != nil {
		t.Fatal(err)
	}
	if err := store.ResourceSnapshots().Put(ctx, ResourceSnapshot{
		ID: uuid.NewString(), TaskID: protectedTask.ID, Kind: "service-intercept",
		Namespace: "development", Name: "api", Data: json.RawMessage(`{"selector":{"app":"api"}}`), CreatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err = store.Sessions().DeleteExpired(ctx, now, 1)
	if err != nil || deleted != 0 {
		t.Fatalf("session with rollback snapshot was deleted: count=%d err=%v", deleted, err)
	}
	if _, err := store.ResourceSnapshots().DeleteByTask(ctx, protectedTask.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err = store.Sessions().DeleteExpired(ctx, now, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("restored expired session deletion = %d, %v", deleted, err)
	}
}

func testIdempotencyRepository(t *testing.T, store *Store) {
	ctx := context.Background()
	now := time.Date(2026, 8, 9, 4, 0, 0, 0, time.UTC)
	record := IdempotencyRecord{
		Scope: "identity:one", Key: "request-1", RequestHash: "sha256:aaa",
		ResourceType: "task", ResourceID: uuid.NewString(), Response: json.RawMessage(`{"accepted":true}`),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	createdRecord, created, err := store.Idempotency().Reserve(ctx, record)
	if err != nil || !created || createdRecord.Key != record.Key {
		t.Fatalf("first reserve = %#v, %t, %v", createdRecord, created, err)
	}
	existing, created, err := store.Idempotency().Reserve(ctx, record)
	if err != nil || created || existing.ResourceID != record.ResourceID {
		t.Fatalf("matching reserve = %#v, %t, %v", existing, created, err)
	}
	mismatch := record
	mismatch.RequestHash = "sha256:bbb"
	if _, _, err := store.Idempotency().Reserve(ctx, mismatch); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("mismatched reserve error = %v", err)
	}
	expired := record
	expired.Key = "expired"
	expired.CreatedAt = now.Add(-2 * time.Hour)
	expired.ExpiresAt = now.Add(-time.Hour)
	if _, created, err := store.Idempotency().Reserve(ctx, expired); err != nil || !created {
		t.Fatalf("expired reserve created = %t, %v", created, err)
	}
	deleted, err := store.Idempotency().DeleteExpired(ctx, now, 1)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted idempotency records = %d, %v", deleted, err)
	}
}

func testAuditRepository(t *testing.T, store *Store) {
	ctx := context.Background()
	identity := createTestIdentity(t, store.Identities(), "audit-user")
	now := time.Date(2026, 8, 9, 5, 0, 0, 0, time.UTC)
	for index, action := range []string{"session.create", "task.create", "task.stop"} {
		if err := store.Audit().Append(ctx, AuditEvent{
			ID: uuid.NewString(), IdentityID: identity.ID, Action: action,
			ResourceType: "task", ResourceID: fmt.Sprintf("resource-%d", index),
			Outcome: "allowed", RequestID: fmt.Sprintf("request-%d", index),
			Metadata: json.RawMessage(`{"safe":true}`), CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.Audit().List(ctx, AuditFilter{
		IdentityID: identity.ID, Action: "task.create", After: now, Before: now.Add(3 * time.Second), Limit: 10,
	})
	if err != nil || len(events) != 1 || events[0].Action != "task.create" {
		t.Fatalf("filtered audit events = %#v, %v", events, err)
	}
}

func testTransactions(t *testing.T, store *Store) {
	ctx := context.Background()
	rollbackID := uuid.NewString()
	sentinel := errors.New("rollback")
	err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		_, err := repositories.Identities().Create(ctx, Identity{ID: rollbackID, Type: "human", DisplayName: "Rollback", Status: "active"})
		if err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("transaction error = %v", err)
	}
	if _, err := store.Identities().GetByID(ctx, rollbackID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back identity lookup = %v", err)
	}
	commitID := uuid.NewString()
	if err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		_, err := repositories.Identities().Create(ctx, Identity{ID: commitID, Type: "human", DisplayName: "Commit", Status: "active"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Identities().GetByID(ctx, commitID); err != nil {
		t.Fatalf("committed identity lookup = %v", err)
	}
	panicID := uuid.NewString()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("transaction panic was swallowed")
			}
		}()
		_ = store.WithinTransaction(ctx, func(repositories Repositories) error {
			_, _ = repositories.Identities().Create(ctx, Identity{ID: panicID, Type: "human", DisplayName: "Panic", Status: "active"})
			panic("stop")
		})
	}()
	if _, err := store.Identities().GetByID(ctx, panicID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("panic transaction identity lookup = %v", err)
	}
}

func createTestIdentity(t *testing.T, repository IdentityRepository, externalID string) Identity {
	t.Helper()
	identity, err := repository.Create(context.Background(), Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: externalID, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

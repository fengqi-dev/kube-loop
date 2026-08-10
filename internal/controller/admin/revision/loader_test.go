package revision

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/google/uuid"
)

func TestPolicyLoaderInstallsPublishedAggregateAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := openLoaderStore(t)
	engine, err := adminauthorization.NewDenyAll()
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewPolicyLoader(store, engine, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); err != nil {
		t.Fatal(err)
	}

	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	principalID := uuid.NewString()
	draft, err := service.CreatePolicyDraft(ctx, PolicyDraftRequest{
		Snapshot: adminauthorization.Snapshot{Version: adminauthorization.CurrentVersion, Assignments: []adminauthorization.Assignment{{
			ID: uuid.NewString(), Role: adminauthorization.RolePlatformAdmin, Subjects: []string{principalID},
		}}},
		IdempotencyKey: "loader-create-key-0001", Reason: "install first admin", RequestID: uuid.NewString(),
		Actor: Actor{Authentication: adminauthorization.AuthenticationBreakGlass},
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: draft.Change.ID, ExpectedETag: 0, Reason: "publish first admin", RequestID: uuid.NewString(),
		Actor: Actor{Authentication: adminauthorization.AuthenticationBreakGlass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if engine.Revision() != activation.Active.Revision || engine.ETag() != activation.Active.ETag || !engine.Available() {
		t.Fatalf("engine revision/ETag/available = %d/%d/%v", engine.Revision(), engine.ETag(), engine.Available())
	}
	decision := engine.Authorize(ctx, adminauthorization.Subject{ID: principalID}, adminauthorization.Request{
		Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationPublish,
	})
	if !decision.Allowed {
		t.Fatalf("published administrator decision = %#v", decision)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("load after storage close = %v", err)
	}
	if engine.Available() || engine.Authorize(ctx, adminauthorization.Subject{ID: principalID}, adminauthorization.Request{
		Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationPublish,
	}).Allowed {
		t.Fatal("storage failure did not remove database-backed grant")
	}
	if err := loader.Check(ctx); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("loader health = %v", err)
	}
}

func TestPolicyLoaderRejectsRevisionAssignmentMismatch(t *testing.T) {
	ctx := context.Background()
	store := openLoaderStore(t)
	defer store.Close()
	engine, err := adminauthorization.NewDenyAll()
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewPolicyLoader(store, engine, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	revision, err := store.AdminPolicyRevisions().Create(ctx, storage.AdminPolicyRevision{
		ID: uuid.NewString(), Spec: []byte(`{"version":1,"assignments":[{"id":"` + uuid.NewString() + `","role":"platform-admin","groups":["admins"]}]}`),
		ValidationState: storage.RevisionValidationValid, Validation: []byte(`{"valid":true}`),
		CreatedBy: storage.ManagementActorBreakGlass, CreatedAuthenticationType: string(adminauthorization.AuthenticationBreakGlass),
		Reason: "mismatch test", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveManagementRevisions().CompareAndSwap(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID, revision.Revision, 0,
		storage.ManagementActorBreakGlass, string(adminauthorization.AuthenticationBreakGlass), now,
	); err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("mismatched aggregate load = %v", err)
	}
	if engine.Available() {
		t.Fatal("mismatched aggregate left engine available")
	}
}

func openLoaderStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "loader.db"), ControllerReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

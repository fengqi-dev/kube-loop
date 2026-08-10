package revision

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/google/uuid"
)

func TestPolicyRevisionPublishAndRollbackAreTransactional(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	service, _ := New(store)
	service.now = func() time.Time { return now }
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	firstSnapshot := policySnapshot(principal.ID)

	first := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: firstSnapshot, ExpectedETag: 0,
		IdempotencyKey: "policy-create-key-0001", Reason: "establish formal administration",
		RequestID: "request-create-1", Actor: actor,
	})
	replayed, err := service.CreatePolicyDraft(ctx, PolicyDraftRequest{
		Snapshot: firstSnapshot, ExpectedETag: 0,
		IdempotencyKey: "policy-create-key-0001", Reason: "establish formal administration",
		RequestID: "request-create-replay", Actor: actor,
	})
	if err != nil || !replayed.Replayed || replayed.Revision.Revision != first.Revision.Revision {
		t.Fatalf("replayed draft = %#v, %v", replayed, err)
	}
	mismatched := firstSnapshot
	mismatched.Assignments = append([]adminauthorization.Assignment(nil), firstSnapshot.Assignments...)
	mismatched.Assignments[0].Role = adminauthorization.RoleAuditor
	if _, err := service.CreatePolicyDraft(ctx, PolicyDraftRequest{
		Snapshot: mismatched, IdempotencyKey: "policy-create-key-0001", Reason: "establish formal administration",
		RequestID: "request-create-mismatch", Actor: actor,
	}); !errors.Is(err, storage.ErrIdempotencyMismatch) {
		t.Fatalf("idempotency mismatch = %v", err)
	}

	published, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: first.Change.ID, ExpectedETag: 0, IdempotencyKey: "policy-create-key-0001", Reason: "activate first administrators",
		RequestID: "request-publish-1", Actor: actor,
	})
	if err != nil || published.Active.Revision != first.Revision.Revision || published.Active.ETag != 1 {
		t.Fatalf("first publish = %#v, %v", published, err)
	}
	replayedPublish, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: first.Change.ID, ExpectedETag: 0, IdempotencyKey: "policy-create-key-0001", Reason: "retry activate first administrators",
		RequestID: "request-publish-1-retry", Actor: actor,
	})
	if err != nil || !replayedPublish.Replayed || replayedPublish.Active.ETag != 1 {
		t.Fatalf("publish replay = %#v, %v", replayedPublish, err)
	}
	retired, err := store.ManagementState().BootstrapRetired(ctx)
	if err != nil || !retired {
		t.Fatalf("bootstrap retired = %t, %v", retired, err)
	}

	// Stable assignment IDs are intentionally reused across immutable policy
	// revisions so history and audit references keep their identity.
	secondSnapshot := firstSnapshot
	secondSnapshot.Assignments = append([]adminauthorization.Assignment(nil), firstSnapshot.Assignments...)
	secondSnapshot.Assignments = append(secondSnapshot.Assignments, adminauthorization.Assignment{
		ID: uuid.NewString(), Role: adminauthorization.RoleAuditor, Subjects: []string{principal.ID},
	})
	second := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: secondSnapshot, ExpectedETag: 1, IdempotencyKey: "policy-create-key-0002",
		Reason: "add audit visibility", RequestID: "request-create-2", Actor: actor,
	})
	published, err = service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: second.Change.ID, ExpectedETag: 1, IdempotencyKey: "policy-create-key-0002", Reason: "activate audit visibility",
		RequestID: "request-publish-2", Actor: actor,
	})
	if err != nil || published.Active.Revision != second.Revision.Revision || published.Active.ETag != 2 {
		t.Fatalf("second publish = %#v, %v", published, err)
	}
	rolledBack, err := service.RollbackPolicy(ctx, RollbackRequest{
		TargetRevision: first.Revision.Revision, ExpectedETag: 2,
		IdempotencyKey: "policy-rollback-key-1", Reason: "restore known-good policy",
		RequestID: "request-rollback-1", Actor: actor,
	})
	if err != nil || rolledBack.Active.Revision != first.Revision.Revision || rolledBack.Active.ETag != 3 {
		t.Fatalf("rollback = %#v, %v", rolledBack, err)
	}
	replayedRollback, err := service.RollbackPolicy(ctx, RollbackRequest{
		TargetRevision: first.Revision.Revision, ExpectedETag: 2,
		IdempotencyKey: "policy-rollback-key-1", Reason: "restore known-good policy",
		RequestID: "request-rollback-replay", Actor: actor,
	})
	if err != nil || !replayedRollback.Replayed || replayedRollback.Active.ETag != 3 {
		t.Fatalf("rollback replay = %#v, %v", replayedRollback, err)
	}
	if _, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: second.Change.ID, ExpectedETag: 1, IdempotencyKey: "policy-create-key-0002", Reason: "stale republish",
		RequestID: "request-stale", Actor: actor,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish = %v", err)
	}
	events, err := store.Audit().List(ctx, storage.AuditFilter{Limit: 100})
	if err != nil || len(events) != 5 {
		t.Fatalf("audit events = %d, %v", len(events), err)
	}
}

func TestPolicyPublishAuditFailureRollsBackPointerAndBootstrapRetirement(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	service, _ := New(store)
	service.now = func() time.Time { return now }
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	draft := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: policySnapshot(principal.ID), IdempotencyKey: "audit-rollback-key-1",
		Reason: "prepare rollback test", RequestID: "request-draft", Actor: actor,
	})
	collision := uuid.NewString()
	if err := store.Audit().Append(ctx, storage.AuditEvent{
		ID: collision, Action: "seed", Outcome: "success", RequestID: "seed", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service.newID = func() string { return collision }
	if _, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: draft.Change.ID, ExpectedETag: 0, IdempotencyKey: "audit-rollback-key-1", Reason: "must roll back",
		RequestID: "request-publish", Actor: actor,
	}); err == nil {
		t.Fatal("publish succeeded when audit insert failed")
	}
	if _, err := store.ActiveManagementRevisions().Get(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
	); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("rolled-back active pointer lookup = %v", err)
	}
	retired, err := store.ManagementState().BootstrapRetired(ctx)
	if err != nil || retired {
		t.Fatalf("bootstrap retired after rollback = %t, %v", retired, err)
	}
	change, err := store.ConfigChangeRequests().GetByID(ctx, draft.Change.ID)
	if err != nil || change.Status != storage.ChangeStatusValidated {
		t.Fatalf("change after rollback = %#v, %v", change, err)
	}
}

func TestBreakGlassActorCanCreateAndPublishWithoutPrincipalRow(t *testing.T) {
	store := openTestStore(t)
	service, _ := New(store)
	actor := Actor{Authentication: adminauthorization.AuthenticationBreakGlass}
	principalID := uuid.NewString()
	draft := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: policySnapshot(principalID), IdempotencyKey: "break-glass-policy-key",
		Reason: "recover formal administration", RequestID: "request-break-glass-create", Actor: actor,
	})
	activation, err := service.PublishPolicy(context.Background(), ActivateRequest{
		ChangeID: draft.Change.ID, ExpectedETag: 0, IdempotencyKey: "break-glass-policy-key", Reason: "recover formal administration",
		RequestID: "request-break-glass-publish", Actor: actor,
	})
	if err != nil || activation.Active.UpdatedBy != storage.ManagementActorBreakGlass ||
		activation.Active.UpdatedAuthenticationType != "break-glass" {
		t.Fatalf("break-glass activation = %#v, %v", activation, err)
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Action: "admin.policy.publish"})
	if err != nil || len(events) != 1 || events[0].PrincipalID != "" ||
		!json.Valid(events[0].Metadata) {
		t.Fatalf("break-glass audit = %#v, %v", events, err)
	}
}

func createPolicyDraft(t *testing.T, service *Service, request PolicyDraftRequest) PolicyDraft {
	t.Helper()
	draft, err := service.CreatePolicyDraft(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func policySnapshot(principalID string) adminauthorization.Snapshot {
	return adminauthorization.Snapshot{
		Version: adminauthorization.CurrentVersion,
		Assignments: []adminauthorization.Assignment{{
			ID: uuid.NewString(), Role: adminauthorization.RolePlatformAdmin, Subjects: []string{principalID},
		}},
	}
}

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createPrincipal(t *testing.T, store *storage.Store, now time.Time) storage.Principal {
	t.Helper()
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: uuid.NewString(), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

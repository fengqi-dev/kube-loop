package managementconfig

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func TestPolicyConfigPublishIsTransactional(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	service, _ := New(store)
	service.now = func() time.Time { return now }
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	firstSnapshot := policySnapshot(principal.ID)

	first := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot:       firstSnapshot,
		IdempotencyKey: "policy-create-key-0001", Reason: "establish formal administration",
		RequestID: "request-create-1", Actor: actor,
	})
	replayed, err := service.CreatePolicyDraft(ctx, PolicyDraftRequest{
		Snapshot:       firstSnapshot,
		IdempotencyKey: "policy-create-key-0001", Reason: "establish formal administration",
		RequestID: "request-create-replay", Actor: actor,
	})
	if err != nil || !replayed.Replayed || replayed.Config.ID != first.Config.ID {
		t.Fatalf("replayed draft = %#v, %v", replayed, err)
	}
	mismatched := firstSnapshot
	mismatched.Bindings = append([]adminauthorization.Binding(nil), firstSnapshot.Bindings...)
	mismatched.Bindings[0].RoleID = adminauthorization.RoleAuditor
	if _, err := service.CreatePolicyDraft(ctx, PolicyDraftRequest{
		Snapshot: mismatched, IdempotencyKey: "policy-create-key-0001", Reason: "establish formal administration",
		RequestID: "request-create-mismatch", Actor: actor,
	}); !errors.Is(err, storage.ErrIdempotencyMismatch) {
		t.Fatalf("idempotency mismatch = %v", err)
	}

	published, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: first.Change.ID, IdempotencyKey: "policy-create-key-0001", Reason: "activate first administrators",
		RequestID: "request-publish-1", Actor: actor,
	})
	if err != nil || published.Active.ObjectID != first.Config.ID {
		t.Fatalf("first publish = %#v, %v", published, err)
	}
	replayedPublish, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: first.Change.ID, IdempotencyKey: "policy-create-key-0001", Reason: "retry activate first administrators",
		RequestID: "request-publish-1-retry", Actor: actor,
	})
	if err != nil || !replayedPublish.Replayed || replayedPublish.Active.ObjectID != first.Config.ID {
		t.Fatalf("publish replay = %#v, %v", replayedPublish, err)
	}
	retired, err := store.ManagementState().BootstrapRetired(ctx)
	if err != nil || !retired {
		t.Fatalf("bootstrap retired = %t, %v", retired, err)
	}

	// Stable binding IDs are intentionally reused across policy configurations
	// so audit references keep their identity.
	secondSnapshot := firstSnapshot
	secondSnapshot.Bindings = append([]adminauthorization.Binding(nil), firstSnapshot.Bindings...)
	secondSnapshot.Bindings = append(secondSnapshot.Bindings, platformBinding(uuid.NewString(), adminauthorization.RoleAuditor, principal.ID))
	second := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: secondSnapshot, IdempotencyKey: "policy-create-key-0002",
		Reason: "add audit visibility", RequestID: "request-create-2", Actor: actor,
	})
	published, err = service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: second.Change.ID, IdempotencyKey: "policy-create-key-0002", Reason: "activate audit visibility",
		RequestID: "request-publish-2", Actor: actor,
	})
	if err != nil || published.Active.ObjectID != second.Config.ID {
		t.Fatalf("second publish = %#v, %v", published, err)
	}
	events, err := store.Audit().List(ctx, storage.AuditFilter{Limit: 100})
	if err != nil || len(events) != 4 {
		t.Fatalf("audit events = %d, %v", len(events), err)
	}
}

func TestPolicyPublishAuditFailureAbortsPointerAndBootstrapRetirement(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 16, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	service, _ := New(store)
	service.now = func() time.Time { return now }
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	draft := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: policySnapshot(principal.ID), IdempotencyKey: "audit-failure-key-1",
		Reason: "prepare audit failure test", RequestID: "request-draft", Actor: actor,
	})
	collision := uuid.NewString()
	if err := store.Audit().Append(ctx, storage.AuditEvent{
		ID: collision, Action: "seed", Outcome: "success", RequestID: "seed", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	service.newID = func() string { return collision }
	if _, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: draft.Change.ID, IdempotencyKey: "audit-failure-key-1", Reason: "must remain atomic",
		RequestID: "request-publish", Actor: actor,
	}); err == nil {
		t.Fatal("publish succeeded when audit insert failed")
	}
	if _, err := store.ActiveManagementConfigs().Get(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
	); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("active pointer after aborted publish = %v", err)
	}
	retired, err := store.ManagementState().BootstrapRetired(ctx)
	if err != nil || retired {
		t.Fatalf("bootstrap retired after aborted publish = %t, %v", retired, err)
	}
	change, err := store.ConfigChangeRequests().GetByID(ctx, draft.Change.ID)
	if err != nil || change.Status != storage.ChangeStatusValidated {
		t.Fatalf("change after aborted publish = %#v, %v", change, err)
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
		ChangeID: draft.Change.ID, IdempotencyKey: "break-glass-policy-key", Reason: "recover formal administration",
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
		Version:  adminauthorization.CurrentVersion,
		Bindings: []adminauthorization.Binding{platformBinding(uuid.NewString(), adminauthorization.RolePlatformAdmin, principalID)},
	}
}

func platformBinding(id string, role adminauthorization.Role, principalID string) adminauthorization.Binding {
	return adminauthorization.Binding{ID: id, Subject: adminauthorization.SubjectRef{Type: adminauthorization.SubjectPrincipal, PrincipalID: principalID}, RoleID: role, Scope: adminauthorization.BindingScope{Type: adminauthorization.ScopePlatform}, ManagedBy: adminauthorization.ManagedByPlatform}
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

package managementconfig

import (
	"context"
	"errors"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func TestApplyDelegationConstrainsScopeAndCreatesConfig(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	service, _ := New(store)
	service.now = func() time.Time { return now }
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	draft := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: policySnapshot(principal.ID), IdempotencyKey: "delegation-initial-policy-key",
		Reason: "establish delegation administrator", RequestID: uuid.NewString(), Actor: actor,
	})
	published, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: draft.Change.ID, IdempotencyKey: "delegation-initial-policy-key",
		Reason: "publish delegation administrator", RequestID: uuid.NewString(), Actor: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingID := uuid.NewString()
	delegated, err := service.ApplyDelegation(ctx, DelegationRequest{
		Binding: &adminauthorization.Binding{
			ID: bindingID, Subject: adminauthorization.SubjectRef{Type: adminauthorization.SubjectGroup, ProviderID: "auth0", GroupName: "team-a-developers"},
			RoleID: adminauthorization.RoleNamespaceViewer,
			// Caller-controlled fields must be ignored.
			ManagedBy: adminauthorization.ManagedByPlatform,
			Scope:     adminauthorization.BindingScope{Type: adminauthorization.ScopePlatform},
		},
		Namespace:      "team-a",
		IdempotencyKey: "delegation-team-a-read-key",
		Reason:         "delegate namespace read access", RequestID: uuid.NewString(), Actor: actor,
	})
	if err != nil || delegated.Active.ObjectID == published.Active.ObjectID {
		t.Fatalf("delegation = %#v, %v", delegated, err)
	}
	state, err := service.CurrentPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var stored adminauthorization.Binding
	for _, binding := range state.Snapshot.Bindings {
		if binding.ID == bindingID {
			stored = binding
		}
	}
	if stored.ManagedBy != adminauthorization.ManagedByDelegated || stored.CreatedBy != principal.ID ||
		stored.Scope.Type != adminauthorization.ScopeNamespaces || len(stored.Scope.Names) != 1 || stored.Scope.Names[0] != "team-a" {
		t.Fatalf("stored delegated binding = %#v", stored)
	}
	if _, err := store.AdminPolicyConfigs().Get(ctx, published.Active.ObjectID); err != nil {
		t.Fatalf("prior configuration was lost: %v", err)
	}
	replayed, err := service.ApplyDelegation(ctx, DelegationRequest{
		Binding: &adminauthorization.Binding{
			ID: bindingID, Subject: adminauthorization.SubjectRef{Type: adminauthorization.SubjectGroup, ProviderID: "auth0", GroupName: "team-a-developers"},
			RoleID:    adminauthorization.RoleNamespaceViewer,
			ManagedBy: adminauthorization.ManagedByPlatform,
			Scope:     adminauthorization.BindingScope{Type: adminauthorization.ScopePlatform},
		},
		Namespace:      "team-a",
		IdempotencyKey: "delegation-team-a-read-key",
		Reason:         "delegate namespace read access", RequestID: uuid.NewString(), Actor: actor,
	})
	if err != nil || !replayed.Replayed || replayed.Active.ObjectID != delegated.Active.ObjectID {
		t.Fatalf("delegation replay = %#v, %v", replayed, err)
	}
}

func TestApplyDelegationRejectsEscalationAndCrossNamespaceMutation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	service, _ := New(store)
	service.now = func() time.Time { return now }
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	draft := createPolicyDraft(t, service, PolicyDraftRequest{
		Snapshot: policySnapshot(principal.ID), IdempotencyKey: "delegation-escalation-policy-key",
		Reason: "establish namespace administrator", RequestID: uuid.NewString(), Actor: actor,
	})
	_, err := service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: draft.Change.ID, IdempotencyKey: "delegation-escalation-policy-key",
		Reason: "publish namespace administrator", RequestID: uuid.NewString(), Actor: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyDelegation(ctx, DelegationRequest{
		Binding: &adminauthorization.Binding{
			ID: uuid.NewString(), Subject: adminauthorization.SubjectRef{Type: adminauthorization.SubjectPrincipal, PrincipalID: uuid.NewString()},
			RoleID: adminauthorization.RolePlatformAdmin,
		},
		Namespace:      "team-a",
		IdempotencyKey: "delegation-platform-escalation-key",
		Reason:         "attempt platform escalation", RequestID: uuid.NewString(), Actor: actor,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("platform escalation error = %v", err)
	}

	validID := uuid.NewString()
	_, err = service.ApplyDelegation(ctx, DelegationRequest{
		Binding: &adminauthorization.Binding{
			ID: validID, Subject: adminauthorization.SubjectRef{Type: adminauthorization.SubjectPrincipal, PrincipalID: uuid.NewString()},
			RoleID: adminauthorization.RoleNamespaceViewer,
		}, Namespace: "team-a",
		IdempotencyKey: "delegation-team-a-create-key",
		Reason:         "create team a delegation", RequestID: uuid.NewString(), Actor: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyDelegation(ctx, DelegationRequest{
		DeleteID: validID, Namespace: "team-b",
		IdempotencyKey: "delegation-cross-namespace-delete-key",
		Reason:         "cross namespace delete attempt", RequestID: uuid.NewString(), Actor: actor,
	})
	if !errors.Is(err, ErrConflict) || !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("cross namespace mutation error = %v", err)
	}
}

package managementconfig

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
	customPrincipalID := uuid.NewString()
	draft, err := service.CreatePolicyDraft(ctx, PolicyDraftRequest{
		Snapshot: adminauthorization.Snapshot{
			Version: adminauthorization.CurrentVersion,
			Roles: []adminauthorization.RoleDefinition{{
				ID: "session-reader", DisplayName: "Session reader", Statements: []adminauthorization.Statement{{Effect: adminauthorization.EffectAllow, Capabilities: []adminauthorization.Capability{"platform.sessions.read"}}},
			}},
			Bindings: []adminauthorization.Binding{
				platformBinding(uuid.NewString(), adminauthorization.RolePlatformAdmin, principalID),
				platformBinding(uuid.NewString(), "session-reader", customPrincipalID),
			},
		},
		IdempotencyKey: "loader-create-key-0001", Reason: "install first admin", RequestID: uuid.NewString(),
		Actor: Actor{Authentication: adminauthorization.AuthenticationBreakGlass},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PublishPolicy(ctx, ActivateRequest{
		ChangeID: draft.Change.ID, IdempotencyKey: "loader-create-key-0001",
		Reason: "publish first admin", RequestID: uuid.NewString(),
		Actor: Actor{Authentication: adminauthorization.AuthenticationBreakGlass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !engine.Available() {
		t.Fatal("engine is unavailable after loading the published configuration")
	}
	decision := engine.Authorize(ctx, adminauthorization.Subject{ID: principalID}, adminauthorization.Request{
		Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationPublish,
	})
	if !decision.Allowed {
		t.Fatalf("published administrator decision = %#v", decision)
	}
	customDecision := engine.Authorize(ctx, adminauthorization.Subject{ID: customPrincipalID}, adminauthorization.Request{
		Resource: adminauthorization.ResourceSession, Operation: adminauthorization.OperationRead,
	})
	if !customDecision.Allowed || len(customDecision.MatchingAllow) != 1 || customDecision.MatchingAllow[0].RoleID != "session-reader" {
		t.Fatalf("published custom role decision = %#v", customDecision)
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

func TestPolicyLoaderRejectsInvalidConfig(t *testing.T) {
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
	config, err := store.AdminPolicyConfigs().Create(ctx, storage.AdminPolicyConfig{
		ID: uuid.NewString(), Spec: []byte(`{"version":2,"bindings":[{"id":"bad","roleId":"platform-admin"}]}`),
		ValidationState: storage.ConfigValidationValid, Validation: []byte(`{"valid":true}`),
		CreatedBy: storage.ManagementActorBreakGlass, CreatedAuthenticationType: string(adminauthorization.AuthenticationBreakGlass),
		Reason: "mismatch test", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveManagementConfigs().Set(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID, config.ID,
		storage.ManagementActorBreakGlass, string(adminauthorization.AuthenticationBreakGlass), now,
	); err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("invalid aggregate load = %v", err)
	}
	if engine.Available() {
		t.Fatal("mismatched aggregate left engine available")
	}
}

func openLoaderStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "loader.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

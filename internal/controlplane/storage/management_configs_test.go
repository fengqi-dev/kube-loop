package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testManagementConfigRepositories(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	creator := createTestPrincipal(t, store.Principals(), "management-config-"+uuid.NewString())

	firstPolicy, err := store.AdminPolicyConfigs().Create(ctx, AdminPolicyConfig{
		ID: uuid.NewString(), Spec: json.RawMessage(`{"version":2,"roles":[],"bindings":[]}`),
		ValidationState: ConfigValidationValid, Validation: json.RawMessage(`{"valid":true}`),
		CreatedBy: creator.ID, CreatedAuthenticationType: "normal",
		Reason: "establish formal administrators", CreatedAt: now,
	})
	if err != nil || firstPolicy.SpecHash != jsonSHA256(firstPolicy.Spec) {
		t.Fatalf("first policy = %#v, %v", firstPolicy, err)
	}
	storedPolicy, err := store.AdminPolicyConfigs().Get(ctx, firstPolicy.ID)
	if err != nil || storedPolicy.SpecHash != firstPolicy.SpecHash || !bytes.Equal(storedPolicy.Spec, firstPolicy.Spec) {
		t.Fatalf("stored policy = %#v, %v", storedPolicy, err)
	}
	active, err := store.ActiveManagementConfigs().Set(ctx, ManagementConfigurationPolicy, ManagementPolicyID,
		firstPolicy.ID, creator.ID, "normal", now.Add(time.Second))
	if err != nil || active.ObjectID != firstPolicy.ID {
		t.Fatalf("active policy = %#v, %v", active, err)
	}

	draft, err := store.AdminPolicyConfigs().Create(ctx, AdminPolicyConfig{
		ID: uuid.NewString(), Spec: json.RawMessage(`{"version":1}`), ValidationState: ConfigValidationDraft,
		CreatedBy: creator.ID, CreatedAuthenticationType: "normal", Reason: "draft must not publish", CreatedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveManagementConfigs().Set(ctx, ManagementConfigurationPolicy, ManagementPolicyID,
		draft.ID, creator.ID, "normal", now.Add(3*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft activation = %v", err)
	}

	provider, err := store.ProviderConfigs().Create(ctx, ProviderConfig{
		ID: uuid.NewString(), ProviderID: "corporate", ProviderType: "oidc",
		Config: json.RawMessage(`{"issuer":"https://id.example","clientId":"kubeloop"}`), SecretAliases: json.RawMessage(`{}`),
		ValidationState: ConfigValidationValid, CreatedBy: creator.ID, CreatedAuthenticationType: "normal",
		Reason: "publish corporate provider", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	providerActive, err := store.ActiveManagementConfigs().Set(ctx, ManagementConfigurationProvider, "corporate",
		provider.ID, creator.ID, "normal", now.Add(time.Second))
	if err != nil || providerActive.ObjectID != provider.ID {
		t.Fatalf("active provider = %#v, %v", providerActive, err)
	}

	idempotency := sha256.Sum256([]byte("management-change-idempotency"))
	change := ConfigChangeRequest{
		ID: uuid.NewString(), ConfigurationType: ManagementConfigurationPolicy, ConfigurationID: ManagementPolicyID,
		BaseObjectID: firstPolicy.ID, ProposedObjectID: firstPolicy.ID, Status: ChangeStatusDraft,
		IdempotencyHash: idempotency[:], RequestHash: jsonSHA256(json.RawMessage(`{"request":1}`)),
		RequestedBy: creator.ID, RequestedAuthenticationType: "normal", Reason: "publish policy configuration",
		CreatedAt: now.Add(6 * time.Second), UpdatedAt: now.Add(6 * time.Second),
	}
	if err := store.ConfigChangeRequests().Create(ctx, change); err != nil {
		t.Fatal(err)
	}
	storedChange, err := store.ConfigChangeRequests().GetByIdempotencyHash(
		ctx, creator.ID, "normal", ManagementConfigurationPolicy, ManagementPolicyID, idempotency[:],
	)
	if err != nil || storedChange.ID != change.ID || storedChange.ProposedObjectID != firstPolicy.ID {
		t.Fatalf("idempotent change = %#v, %v", storedChange, err)
	}
	if err := store.ConfigChangeRequests().UpdateStatus(ctx, change.ID, ChangeStatusDraft, ChangeStatusValidated,
		json.RawMessage(`{"valid":true}`), now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigChangeRequests().UpdateStatus(ctx, change.ID, ChangeStatusValidated, ChangeStatusPublished,
		json.RawMessage(`{"valid":true}`), now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigChangeRequests().UpdateStatus(ctx, change.ID, ChangeStatusPublished, ChangeStatusValidated,
		nil, now.Add(9*time.Second)); err == nil {
		t.Fatal("terminal change request transitioned")
	}
}

func TestManagementConfigTransactionAbortsAggregate(t *testing.T) {
	store := openSQLiteTestStore(t, t.TempDir()+"/abort.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	creator := createTestPrincipal(t, store.Principals(), "management-abort")
	configID := uuid.NewString()
	err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		_, err := repositories.AdminPolicyConfigs().Create(ctx, AdminPolicyConfig{
			ID: configID, Spec: json.RawMessage(`{"version":1}`), ValidationState: ConfigValidationValid,
			CreatedBy: creator.ID, CreatedAuthenticationType: "normal", Reason: "exercise transaction abort", CreatedAt: now,
		})
		if err != nil {
			return err
		}
		return errors.New("force aggregate abort")
	})
	if err == nil {
		t.Fatal("invalid aggregate transaction committed")
	}
	if _, err := store.AdminPolicyConfigs().Get(ctx, configID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("aborted policy lookup = %v", err)
	}
}

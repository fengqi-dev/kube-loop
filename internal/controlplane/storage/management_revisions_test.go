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

func testManagementRevisionRepositories(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	creator := createTestPrincipal(t, store.Principals(), "management-revision-"+uuid.NewString())

	var firstPolicy AdminPolicyRevision
	err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		var createErr error
		firstPolicy, createErr = repositories.AdminPolicyRevisions().Create(ctx, AdminPolicyRevision{
			ID: uuid.NewString(), Spec: json.RawMessage(`{"version":2,"roles":[],"bindings":[]}`),
			ValidationState: RevisionValidationValid, Validation: json.RawMessage(`{"valid":true}`),
			CreatedBy: creator.ID, CreatedAuthenticationType: "normal",
			Reason: "establish formal administrators", CreatedAt: now,
		})
		return createErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstPolicy.Revision == 0 || firstPolicy.SpecHash != jsonSHA256(firstPolicy.Spec) {
		t.Fatalf("first policy = %#v", firstPolicy)
	}
	storedPolicy, err := store.AdminPolicyRevisions().Get(ctx, firstPolicy.Revision)
	if err != nil || storedPolicy.SpecHash != firstPolicy.SpecHash || !bytes.Equal(storedPolicy.Spec, firstPolicy.Spec) {
		t.Fatalf("stored policy = %#v, %v", storedPolicy, err)
	}
	active, err := store.ActiveManagementRevisions().CompareAndSwap(
		ctx, ManagementConfigurationPolicy, ManagementPolicyID, firstPolicy.Revision, 0,
		creator.ID, "normal", now.Add(time.Second),
	)
	if err != nil || active.ETag != 1 || active.Revision != firstPolicy.Revision {
		t.Fatalf("first active pointer = %#v, %v", active, err)
	}
	if _, err := store.ActiveManagementRevisions().CompareAndSwap(
		ctx, ManagementConfigurationPolicy, ManagementPolicyID, firstPolicy.Revision, 0,
		creator.ID, "normal", now.Add(2*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale initial publish = %v", err)
	}

	draft, err := store.AdminPolicyRevisions().Create(ctx, AdminPolicyRevision{
		ID: uuid.NewString(), Spec: json.RawMessage(`{"version":1}`), ValidationState: RevisionValidationDraft,
		CreatedBy: creator.ID, CreatedAuthenticationType: "normal",
		Reason: "draft must not publish", CreatedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveManagementRevisions().CompareAndSwap(
		ctx, ManagementConfigurationPolicy, ManagementPolicyID, draft.Revision, active.ETag,
		creator.ID, "normal", now.Add(3*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft activation = %v", err)
	}
	secondPolicy, err := store.AdminPolicyRevisions().Create(ctx, AdminPolicyRevision{
		ID: uuid.NewString(), Spec: json.RawMessage(`{"version":2}`), ValidationState: RevisionValidationValid,
		CreatedBy: creator.ID, CreatedAuthenticationType: "normal",
		Reason: "publish second policy", CreatedAt: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err = store.ActiveManagementRevisions().CompareAndSwap(
		ctx, ManagementConfigurationPolicy, ManagementPolicyID, secondPolicy.Revision, 1,
		creator.ID, "normal", now.Add(4*time.Second),
	)
	if err != nil || active.ETag != 2 || active.Revision != secondPolicy.Revision {
		t.Fatalf("second active pointer = %#v, %v", active, err)
	}
	active, err = store.ActiveManagementRevisions().CompareAndSwap(
		ctx, ManagementConfigurationPolicy, ManagementPolicyID, firstPolicy.Revision, 2,
		creator.ID, "normal", now.Add(5*time.Second),
	)
	if err != nil || active.ETag != 3 || active.Revision != firstPolicy.Revision {
		t.Fatalf("rollback pointer = %#v, %v", active, err)
	}

	if _, err := store.ProviderConfigRevisions().Create(ctx, ProviderConfigRevision{
		ID: uuid.NewString(), ProviderID: "database-secret", ProviderType: "oidc",
		Config:        json.RawMessage(`{"issuer":"https://id.example","clientSecret":"plaintext"}`),
		SecretAliases: json.RawMessage(`{"client-secret":"oidc-secret"}`), ValidationState: RevisionValidationValid,
		CreatedBy: creator.ID, CreatedAuthenticationType: "normal", Reason: "must reject plaintext", CreatedAt: now,
	}); err != nil {
		t.Fatalf("provider database Secret was rejected: %v", err)
	}
	provider, err := store.ProviderConfigRevisions().Create(ctx, ProviderConfigRevision{
		ID: uuid.NewString(), ProviderID: "corporate", ProviderType: "oidc",
		Config:        json.RawMessage(`{"issuer":"https://id.example","clientId":"kubeloop"}`),
		SecretAliases: json.RawMessage(`{"client-secret":"oidc-secret"}`), ValidationState: RevisionValidationValid,
		CreatedBy: creator.ID, CreatedAuthenticationType: "normal", Reason: "publish corporate provider", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	providerActive, err := store.ActiveManagementRevisions().CompareAndSwap(
		ctx, ManagementConfigurationProvider, "corporate", provider.Revision, 0,
		creator.ID, "normal", now.Add(time.Second),
	)
	if err != nil || providerActive.ETag != 1 {
		t.Fatalf("provider active pointer = %#v, %v", providerActive, err)
	}
	providerPointers, err := store.ActiveManagementRevisions().List(ctx, ManagementConfigurationProvider)
	if err != nil || len(providerPointers) != 1 || providerPointers[0].ConfigurationID != "corporate" {
		t.Fatalf("provider active pointers = %#v, %v", providerPointers, err)
	}

	idempotency := sha256.Sum256([]byte("management-change-idempotency"))
	change := ConfigChangeRequest{
		ID: uuid.NewString(), ConfigurationType: ManagementConfigurationPolicy, ConfigurationID: ManagementPolicyID,
		BaseRevision: firstPolicy.Revision, BaseETag: active.ETag, ProposedRevision: secondPolicy.Revision,
		Status: ChangeStatusDraft, IdempotencyHash: idempotency[:], RequestHash: jsonSHA256(json.RawMessage(`{"request":1}`)),
		RequestedBy:                 creator.ID,
		RequestedAuthenticationType: "normal",
		Reason:                      "re-publish second policy", CreatedAt: now.Add(6 * time.Second), UpdatedAt: now.Add(6 * time.Second),
	}
	if err := store.ConfigChangeRequests().Create(ctx, change); err != nil {
		t.Fatal(err)
	}
	storedChange, err := store.ConfigChangeRequests().GetByIdempotencyHash(
		ctx, creator.ID, "normal", ManagementConfigurationPolicy, ManagementPolicyID, idempotency[:],
	)
	if err != nil || storedChange.ID != change.ID {
		t.Fatalf("idempotent change = %#v, %v", storedChange, err)
	}
	transitions := []struct {
		from, to string
	}{
		{ChangeStatusDraft, ChangeStatusValidated},
		{ChangeStatusValidated, ChangeStatusPublished},
		{ChangeStatusPublished, ChangeStatusRolledBack},
	}
	for index, transition := range transitions {
		if err := store.ConfigChangeRequests().UpdateStatus(
			ctx, change.ID, transition.from, transition.to, json.RawMessage(`{"valid":true}`),
			now.Add(time.Duration(7+index)*time.Second),
		); err != nil {
			t.Fatalf("transition %s -> %s: %v", transition.from, transition.to, err)
		}
	}
	if err := store.ConfigChangeRequests().UpdateStatus(
		ctx, change.ID, ChangeStatusRolledBack, ChangeStatusPublished, nil, now.Add(11*time.Second),
	); err == nil {
		t.Fatal("terminal change request transitioned")
	}
}

func TestManagementRevisionTransactionRollsBackAggregate(t *testing.T) {
	store := openSQLiteTestStore(t, t.TempDir()+"/rollback.db")
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	creator := createTestPrincipal(t, store.Principals(), "management-rollback")
	revisionID := uuid.NewString()
	var revisionNumber uint64
	err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		revision, err := repositories.AdminPolicyRevisions().Create(ctx, AdminPolicyRevision{
			ID: revisionID, Spec: json.RawMessage(`{"version":1}`), ValidationState: RevisionValidationValid,
			CreatedBy: creator.ID, CreatedAuthenticationType: "normal", Reason: "exercise rollback", CreatedAt: now,
		})
		if err != nil {
			return err
		}
		revisionNumber = revision.Revision
		return errors.New("force aggregate rollback")
	})
	if err == nil {
		t.Fatal("invalid aggregate transaction committed")
	}
	if _, err := store.AdminPolicyRevisions().Get(ctx, revisionNumber); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back policy lookup = %v", err)
	}
}

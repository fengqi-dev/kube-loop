package revision

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type providerLifecycleStub struct {
	validateErr error
	prepareErr  error
	validations int
	prepared    int
	installed   []ProviderCandidate
}

func (stub *providerLifecycleStub) Validate(context.Context, ProviderCandidate) (json.RawMessage, error) {
	stub.validations++
	return json.RawMessage(`{"valid":true,"connectivity":"ready"}`), stub.validateErr
}

func (stub *providerLifecycleStub) Prepare(_ context.Context, candidate ProviderCandidate) (func(), error) {
	stub.prepared++
	if stub.prepareErr != nil {
		return nil, stub.prepareErr
	}
	return func() { stub.installed = append(stub.installed, candidate) }, nil
}

func TestProviderRevisionWorkflowValidatesBeforeAtomicPublishAndRollback(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	principal := createPrincipal(t, store, now)
	actor := Actor{PrincipalID: principal.ID, Authentication: adminauthorization.AuthenticationNormal}
	lifecycle := &providerLifecycleStub{}
	service, err := NewProviderService(store, lifecycle, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	firstRequest := ProviderDraftRequest{
		Candidate: ProviderCandidate{
			ID: "corporate", Type: "oidc",
			Config:        json.RawMessage(`{"issuer":"https://id.example","clientId":"kubeloop"}`),
			SecretAliases: json.RawMessage(`{"client-secret":"corporate-oidc"}`),
		},
		IdempotencyKey: "provider-create-key-001", Reason: "create corporate identity provider",
		RequestID: "provider-create-1", Actor: actor,
	}
	first, err := service.CreateDraft(ctx, firstRequest)
	if err != nil || first.Revision.Revision == 0 || first.Change.Status != storage.ChangeStatusValidated || lifecycle.validations != 1 {
		t.Fatalf("first Provider draft=%#v validations=%d error=%v", first, lifecycle.validations, err)
	}
	lifecycle.validateErr = errors.New("upstream unavailable")
	replay, err := service.CreateDraft(ctx, firstRequest)
	if err != nil || !replay.Replayed || lifecycle.validations != 1 {
		t.Fatalf("Provider draft replay=%#v validations=%d error=%v", replay, lifecycle.validations, err)
	}
	lifecycle.validateErr = nil

	if _, err := service.Publish(ctx, ProviderActivateRequest{
		ProviderID: "corporate", ChangeID: first.Change.ID, IdempotencyKey: "provider-wrong-key-001",
		Reason: "publish corporate identity provider", RequestID: "provider-publish-wrong", Actor: actor,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong Provider publish key error=%v", err)
	}
	if len(lifecycle.installed) != 0 {
		t.Fatal("failed publish mutated the live Provider Registry")
	}
	published, err := service.Publish(ctx, ProviderActivateRequest{
		ProviderID: "corporate", ChangeID: first.Change.ID, IdempotencyKey: firstRequest.IdempotencyKey,
		Reason: "publish corporate identity provider", RequestID: "provider-publish-1", Actor: actor,
	})
	if err != nil || published.Active.ETag != 1 || len(lifecycle.installed) != 1 {
		t.Fatalf("first Provider publish=%#v installed=%d error=%v", published, len(lifecycle.installed), err)
	}
	current, err := service.Current(ctx, "corporate")
	if err != nil || !current.Active || current.Revision.Revision != first.Revision.Revision {
		t.Fatalf("current Provider=%#v error=%v", current, err)
	}

	secondRequest := firstRequest
	secondRequest.ExpectedETag = 1
	secondRequest.IdempotencyKey = "provider-create-key-002"
	secondRequest.RequestID = "provider-create-2"
	secondRequest.Reason = "change corporate provider display name"
	secondRequest.Candidate.Config = json.RawMessage(`{"issuer":"https://id.example","clientId":"kubeloop","displayName":"Corporate"}`)
	second, err := service.CreateDraft(ctx, secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.prepareErr = errors.New("connectivity failed")
	if _, err := service.Publish(ctx, ProviderActivateRequest{
		ProviderID: "corporate", ChangeID: second.Change.ID, ExpectedETag: 1, IdempotencyKey: secondRequest.IdempotencyKey,
		Reason: "publish display name change", RequestID: "provider-publish-failed", Actor: actor,
	}); err == nil {
		t.Fatal("Provider publish succeeded when runtime preparation failed")
	}
	current, _ = service.Current(ctx, "corporate")
	if current.Pointer.ETag != 1 || current.Revision.Revision != first.Revision.Revision || len(lifecycle.installed) != 1 {
		t.Fatalf("failed preparation replaced current Provider: %#v installed=%d", current, len(lifecycle.installed))
	}
	lifecycle.prepareErr = nil
	secondPublished, err := service.Publish(ctx, ProviderActivateRequest{
		ProviderID: "corporate", ChangeID: second.Change.ID, ExpectedETag: 1, IdempotencyKey: secondRequest.IdempotencyKey,
		Reason: "publish display name change", RequestID: "provider-publish-2", Actor: actor,
	})
	if err != nil || secondPublished.Active.ETag != 2 || len(lifecycle.installed) != 2 {
		t.Fatalf("second Provider publish=%#v installed=%d error=%v", secondPublished, len(lifecycle.installed), err)
	}
	rolledBack, err := service.Rollback(ctx, ProviderRollbackRequest{
		ProviderID: "corporate", TargetRevision: first.Revision.Revision, ExpectedETag: 2,
		IdempotencyKey: "provider-rollback-key-01", Reason: "restore known good identity provider",
		RequestID: "provider-rollback-1", Actor: actor,
	})
	if err != nil || rolledBack.Active.ETag != 3 || rolledBack.Active.Revision != first.Revision.Revision || len(lifecycle.installed) != 3 {
		t.Fatalf("Provider rollback=%#v installed=%d error=%v", rolledBack, len(lifecycle.installed), err)
	}
	replayedRollback, err := service.Rollback(ctx, ProviderRollbackRequest{
		ProviderID: "corporate", TargetRevision: first.Revision.Revision, ExpectedETag: 2,
		IdempotencyKey: "provider-rollback-key-01", Reason: "restore known good identity provider",
		RequestID: "provider-rollback-retry", Actor: actor,
	})
	if err != nil || !replayedRollback.Replayed || len(lifecycle.installed) != 4 {
		t.Fatalf("Provider rollback replay=%#v installed=%d error=%v", replayedRollback, len(lifecycle.installed), err)
	}
}

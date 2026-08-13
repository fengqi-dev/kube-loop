// Package revision coordinates immutable Management Plane configuration
// revisions, optimistic active pointers, bootstrap retirement and audit in one
// database transaction.
package revision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest = errors.New("management revision request is invalid")
	ErrConflict       = errors.New("management revision conflict")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type Actor struct {
	PrincipalID    string
	Authentication adminauthorization.AuthenticationType
}

type PolicyDraftRequest struct {
	Snapshot       adminauthorization.Snapshot
	ExpectedETag   uint64
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type PolicyDraft struct {
	Revision storage.AdminPolicyRevision
	Change   storage.ConfigChangeRequest
	Replayed bool
}

// PolicyState is the verified aggregate behind the active management policy
// pointer. An empty installation is represented explicitly so a bootstrap
// administrator can create the first revision with ETag 0.
type PolicyState struct {
	Active   bool
	Pointer  storage.ActiveManagementRevision
	Revision storage.AdminPolicyRevision
	Snapshot adminauthorization.Snapshot
}

type ActivateRequest struct {
	ChangeID       string
	ExpectedETag   uint64
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type RollbackRequest struct {
	TargetRevision uint64
	ExpectedETag   uint64
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type Activation struct {
	Active   storage.ActiveManagementRevision
	ChangeID string
	Replayed bool
}

type Service struct {
	store Store
	now   func() time.Time
	newID func() string
}

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("management revision storage is required")
	}
	return &Service{store: store, now: time.Now, newID: uuid.NewString}, nil
}

func (service *Service) CurrentPolicy(ctx context.Context) (PolicyState, error) {
	active, err := service.store.ActiveManagementRevisions().Get(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
	)
	if errors.Is(err, storage.ErrNotFound) {
		return PolicyState{Snapshot: adminauthorization.Snapshot{
			Version: adminauthorization.CurrentVersion, Roles: []adminauthorization.RoleDefinition{}, Bindings: []adminauthorization.Binding{},
		}}, nil
	}
	if err != nil {
		return PolicyState{}, fmt.Errorf("%w: read active policy", ErrPolicyUnavailable)
	}
	revision, err := service.store.AdminPolicyRevisions().Get(ctx, active.Revision)
	if err != nil || revision.ValidationState != storage.RevisionValidationValid ||
		revision.SpecHash != policySpecHash(revision.Spec) {
		return PolicyState{}, fmt.Errorf("%w: verify active policy revision", ErrPolicyUnavailable)
	}
	snapshot, err := decodePolicySpec(revision.Spec, revision.Revision)
	if err != nil {
		return PolicyState{}, err
	}
	return PolicyState{Active: true, Pointer: active, Revision: revision, Snapshot: snapshot}, nil
}

func (service *Service) CreatePolicyDraft(ctx context.Context, request PolicyDraftRequest) (PolicyDraft, error) {
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return PolicyDraft{}, err
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.RequestID == "" || request.Reason == "" {
		return PolicyDraft{}, ErrInvalidRequest
	}
	snapshot := request.Snapshot
	if snapshot.Version == 0 {
		snapshot.Version = adminauthorization.CurrentVersion
	}
	validationSnapshot := snapshot
	validationSnapshot.Revision = 1
	if _, err := adminauthorization.New(validationSnapshot); err != nil {
		return PolicyDraft{}, ErrInvalidRequest
	}
	spec, err := json.Marshal(struct {
		Version  int                                 `json:"version"`
		Roles    []adminauthorization.RoleDefinition `json:"roles,omitempty"`
		Bindings []adminauthorization.Binding        `json:"bindings"`
	}{Version: snapshot.Version, Roles: snapshot.Roles, Bindings: snapshot.Bindings})
	if err != nil {
		return PolicyDraft{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return PolicyDraft{}, err
	}
	requestHash := hashRequest(struct {
		Spec         json.RawMessage `json:"spec"`
		ExpectedETag uint64          `json:"expectedEtag"`
		Reason       string          `json:"reason"`
	}{Spec: spec, ExpectedETag: request.ExpectedETag, Reason: request.Reason})
	now := service.now().UTC()
	revisionID, changeID, auditID := service.newID(), service.newID(), service.newID()
	result := PolicyDraft{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationPolicy,
			storage.ManagementPolicyID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash {
				return storage.ErrIdempotencyMismatch
			}
			revision, getErr := repositories.AdminPolicyRevisions().Get(ctx, existing.ProposedRevision)
			if getErr != nil {
				return getErr
			}
			result = PolicyDraft{Revision: revision, Change: existing, Replayed: true}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		baseRevision, baseErr := currentBase(ctx, repositories, request.ExpectedETag)
		if baseErr != nil {
			return baseErr
		}
		revision, createErr := repositories.AdminPolicyRevisions().Create(ctx, storage.AdminPolicyRevision{
			ID: revisionID, Spec: spec, ValidationState: storage.RevisionValidationValid,
			Validation: json.RawMessage(`{"valid":true}`), CreatedBy: actorID,
			CreatedAuthenticationType: authenticationType, Reason: request.Reason, CreatedAt: now,
		})
		if createErr != nil {
			return createErr
		}
		if createErr := persistAuthorizationDefinitions(ctx, repositories.AuthorizationDefinitions(), revision.Revision, snapshot, actorID); createErr != nil {
			return createErr
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationPolicy,
			ConfigurationID: storage.ManagementPolicyID, BaseRevision: baseRevision,
			BaseETag: request.ExpectedETag, ProposedRevision: revision.Revision,
			Status: storage.ChangeStatusValidated, IdempotencyHash: idempotencyHash[:], RequestHash: requestHash,
			RequestedBy: actorID, RequestedAuthenticationType: authenticationType,
			Reason: request.Reason, Validation: json.RawMessage(`{"valid":true}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.policy.revision.create",
			ResourceType: "admin-policy", ResourceID: revision.ID, Outcome: "success",
			RequestID: request.RequestID, Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "newRevision": revision.Revision, "baseRevision": baseRevision,
				"baseEtag": request.ExpectedETag, "idempotencyKeyHash": hex.EncodeToString(idempotencyHash[:]),
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = PolicyDraft{Revision: revision, Change: change}
		return nil
	})
	if err != nil {
		return PolicyDraft{}, mapServiceError(err)
	}
	return result, nil
}

func persistAuthorizationDefinitions(
	ctx context.Context,
	repository storage.AuthorizationDefinitionRepository,
	revision uint64,
	snapshot adminauthorization.Snapshot,
	actorID string,
) error {
	roles := append(adminauthorization.BuiltInRoleDefinitions(), snapshot.Roles...)
	for _, role := range roles {
		definition, err := json.Marshal(role)
		if err != nil {
			return err
		}
		if err := repository.CreateRole(ctx, storage.AuthorizationRoleRecord{
			Revision: revision, ID: string(role.ID), Definition: definition,
		}); err != nil {
			return err
		}
	}
	for _, binding := range snapshot.Bindings {
		names, err := json.Marshal(binding.Scope.Names)
		if err != nil {
			return err
		}
		selectors, err := json.Marshal(binding.Scope.LabelSelectors)
		if err != nil {
			return err
		}
		document, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		createdBy := strings.TrimSpace(binding.CreatedBy)
		if createdBy == "" {
			createdBy = actorID
		}
		if err := repository.CreateBinding(ctx, storage.AuthorizationBindingRecord{
			Revision: revision, ID: binding.ID, RoleID: string(binding.RoleID), SubjectType: string(binding.Subject.Type),
			PrincipalID: binding.Subject.PrincipalID, ProviderID: binding.Subject.ProviderID, GroupName: binding.Subject.GroupName,
			ScopeType: string(binding.Scope.Type), NamespaceNames: names, LabelSelectors: selectors,
			ManagedBy: string(binding.ManagedBy), CreatedBy: createdBy, Binding: document,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) PublishPolicy(ctx context.Context, request ActivateRequest) (Activation, error) {
	return service.activatePolicy(ctx, request)
}

func (service *Service) RollbackPolicy(ctx context.Context, request RollbackRequest) (Activation, error) {
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return Activation{}, err
	}
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.TargetRevision == 0 || request.ExpectedETag == 0 || request.RequestID == "" || request.Reason == "" {
		return Activation{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return Activation{}, err
	}
	requestHash := hashRequest(struct {
		TargetRevision uint64 `json:"targetRevision"`
		ExpectedETag   uint64 `json:"expectedEtag"`
		Reason         string `json:"reason"`
	}{request.TargetRevision, request.ExpectedETag, request.Reason})
	now := service.now().UTC()
	revisionID, changeID, auditID := service.newID(), service.newID(), service.newID()
	result := Activation{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationPolicy,
			storage.ManagementPolicyID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash || existing.Status != storage.ChangeStatusRolledBack {
				return storage.ErrIdempotencyMismatch
			}
			result = Activation{
				Active: storage.ActiveManagementRevision{
					ConfigurationType: storage.ManagementConfigurationPolicy,
					ConfigurationID:   storage.ManagementPolicyID, Revision: existing.ProposedRevision,
					ETag: existing.BaseETag + 1, UpdatedBy: actorID,
					UpdatedAuthenticationType: authenticationType, UpdatedAt: existing.UpdatedAt,
				},
				ChangeID: existing.ID, Replayed: true,
			}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		active, err := repositories.ActiveManagementRevisions().Get(
			ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
		)
		if err != nil {
			return err
		}
		if active.ETag != request.ExpectedETag || active.Revision == request.TargetRevision {
			return storage.ErrConflict
		}
		target, err := repositories.AdminPolicyRevisions().Get(ctx, request.TargetRevision)
		if err != nil || !revisionCanBePublished(target, actorID, authenticationType) {
			return storage.ErrConflict
		}
		targetSnapshot, err := decodePolicySpec(target.Spec, target.Revision)
		if err != nil {
			return storage.ErrConflict
		}
		rollbackRevision, err := repositories.AdminPolicyRevisions().Create(ctx, storage.AdminPolicyRevision{
			ID: revisionID, Spec: append(json.RawMessage(nil), target.Spec...),
			ValidationState: storage.RevisionValidationValid,
			Validation:      json.RawMessage(`{"valid":true,"operation":"rollback"}`),
			CreatedBy:       actorID, CreatedAuthenticationType: authenticationType,
			Reason: request.Reason, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := persistAuthorizationDefinitions(ctx, repositories.AuthorizationDefinitions(), rollbackRevision.Revision, targetSnapshot, actorID); err != nil {
			return err
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationPolicy,
			ConfigurationID: storage.ManagementPolicyID, BaseRevision: active.Revision, BaseETag: active.ETag,
			ProposedRevision: rollbackRevision.Revision, Status: storage.ChangeStatusValidated,
			IdempotencyHash: idempotencyHash[:], RequestHash: requestHash,
			RequestedBy: actorID, RequestedAuthenticationType: authenticationType,
			Reason: request.Reason, Validation: json.RawMessage(`{"valid":true,"operation":"rollback"}`),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		next, err := repositories.ActiveManagementRevisions().CompareAndSwap(
			ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
			rollbackRevision.Revision, active.ETag, actorID, authenticationType, now,
		)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(
			ctx, change.ID, storage.ChangeStatusValidated, storage.ChangeStatusPublished, change.Validation, now,
		); err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(
			ctx, change.ID, storage.ChangeStatusPublished, storage.ChangeStatusRolledBack, change.Validation, now,
		); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.policy.rollback", ResourceType: "admin-policy",
			ResourceID: change.ID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "oldRevision": active.Revision, "newRevision": next.Revision,
				"sourceRevision": request.TargetRevision,
				"oldEtag":        active.ETag, "newEtag": next.ETag,
				"idempotencyKeyHash": hex.EncodeToString(idempotencyHash[:]),
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = Activation{Active: next, ChangeID: change.ID}
		return nil
	})
	if err != nil {
		return Activation{}, mapServiceError(err)
	}
	return result, nil
}

func (service *Service) activatePolicy(ctx context.Context, request ActivateRequest) (Activation, error) {
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return Activation{}, err
	}
	request.ChangeID = strings.TrimSpace(request.ChangeID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.ChangeID == "" || request.RequestID == "" || request.Reason == "" {
		return Activation{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return Activation{}, err
	}
	now := service.now().UTC()
	auditID := service.newID()
	result := Activation{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		change, err := repositories.ConfigChangeRequests().GetByID(ctx, request.ChangeID)
		if err != nil {
			return err
		}
		if change.ConfigurationType != storage.ManagementConfigurationPolicy || change.BaseETag != request.ExpectedETag {
			return storage.ErrConflict
		}
		if !bytes.Equal(change.IdempotencyHash, idempotencyHash[:]) {
			return storage.ErrIdempotencyMismatch
		}
		if change.Status == storage.ChangeStatusPublished {
			active, getErr := repositories.ActiveManagementRevisions().Get(
				ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
			)
			if getErr != nil {
				return getErr
			}
			if active.Revision != change.ProposedRevision || active.ETag != change.BaseETag+1 {
				return storage.ErrConflict
			}
			result = Activation{Active: active, ChangeID: change.ID, Replayed: true}
			return nil
		}
		if change.Status != storage.ChangeStatusValidated {
			return storage.ErrConflict
		}
		proposed, err := repositories.AdminPolicyRevisions().Get(ctx, change.ProposedRevision)
		if err != nil {
			return err
		}
		if !revisionCanBePublished(proposed, actorID, authenticationType) {
			return storage.ErrConflict
		}
		active, err := repositories.ActiveManagementRevisions().CompareAndSwap(
			ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
			change.ProposedRevision, request.ExpectedETag, actorID, authenticationType, now,
		)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(
			ctx, change.ID, storage.ChangeStatusValidated, storage.ChangeStatusPublished, change.Validation, now,
		); err != nil {
			return err
		}
		if _, err := repositories.ManagementState().RetireBootstrap(ctx, active.Revision, now); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.policy.publish", ResourceType: "admin-policy",
			ResourceID: change.ID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "oldRevision": change.BaseRevision, "newRevision": active.Revision,
				"oldEtag": request.ExpectedETag, "newEtag": active.ETag,
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = Activation{Active: active, ChangeID: change.ID}
		return nil
	})
	if err != nil {
		return Activation{}, mapServiceError(err)
	}
	return result, nil
}

func currentBase(ctx context.Context, repositories storage.Repositories, expectedETag uint64) (uint64, error) {
	active, err := repositories.ActiveManagementRevisions().Get(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
	)
	if expectedETag == 0 {
		if errors.Is(err, storage.ErrNotFound) {
			return 0, nil
		}
		if err == nil {
			return 0, storage.ErrConflict
		}
		return 0, err
	}
	if err != nil {
		return 0, err
	}
	if active.ETag != expectedETag {
		return 0, storage.ErrConflict
	}
	return active.Revision, nil
}

func normalizeActor(actor Actor) (id, authenticationType, principalID string, err error) {
	authenticationType = string(actor.Authentication)
	switch actor.Authentication {
	case adminauthorization.AuthenticationNormal, adminauthorization.AuthenticationBootstrap:
		if _, parseErr := uuid.Parse(strings.TrimSpace(actor.PrincipalID)); parseErr != nil {
			return "", "", "", ErrInvalidRequest
		}
		id, principalID = strings.TrimSpace(actor.PrincipalID), strings.TrimSpace(actor.PrincipalID)
	case adminauthorization.AuthenticationBreakGlass:
		if strings.TrimSpace(actor.PrincipalID) != "" {
			return "", "", "", ErrInvalidRequest
		}
		id = storage.ManagementActorBreakGlass
	default:
		return "", "", "", ErrInvalidRequest
	}
	return id, authenticationType, principalID, nil
}

func hashIdempotencyKey(value string) ([sha256.Size]byte, error) {
	value = strings.TrimSpace(value)
	if len(value) < 16 || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
		return [sha256.Size]byte{}, ErrInvalidRequest
	}
	return sha256.Sum256([]byte(value)), nil
}

func hashRequest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func auditMetadata(authenticationType, reason string, values map[string]any) json.RawMessage {
	values["authenticationType"] = authenticationType
	values["reason"] = reason
	encoded, _ := json.Marshal(values)
	return encoded
}

func revisionHasPlatformAdmin(revision storage.AdminPolicyRevision) bool {
	snapshot, err := decodePolicySpec(revision.Spec, revision.Revision)
	if err != nil {
		return false
	}
	for _, binding := range snapshot.Bindings {
		if binding.RoleID == adminauthorization.RolePlatformAdmin && binding.Subject.Type == adminauthorization.SubjectPrincipal && binding.Scope.Type == adminauthorization.ScopePlatform {
			return true
		}
	}
	return false
}

func revisionCanBePublished(
	revision storage.AdminPolicyRevision,
	actorID string,
	authentication string,
) bool {
	if !revisionHasPlatformAdmin(revision) {
		return false
	}
	if authentication == string(adminauthorization.AuthenticationBreakGlass) {
		return true
	}
	snapshot, err := decodePolicySpec(revision.Spec, revision.Revision)
	if err != nil {
		return false
	}
	engine, err := adminauthorization.New(snapshot)
	if err != nil {
		return false
	}
	return engine.Authorize(context.Background(), adminauthorization.Subject{ID: actorID}, adminauthorization.Request{
		Capability: "platform.authorization.publish",
	}).Allowed
}

func mapServiceError(err error) error {
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrIdempotencyMismatch) {
		return errors.Join(ErrConflict, err)
	}
	return err
}

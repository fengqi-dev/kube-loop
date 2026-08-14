// Package managementconfig coordinates immutable Management Plane configuration
// objects, active pointers, bootstrap retirement and audit in one transaction.
package managementconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest = errors.New("management configuration request is invalid")
	ErrConflict       = errors.New("management configuration conflict")
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
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type PolicyDraft struct {
	Config   storage.AdminPolicyConfig
	Change   storage.ConfigChangeRequest
	Replayed bool
}

type PolicyState struct {
	Active   bool
	Pointer  storage.ActiveManagementConfig
	Config   storage.AdminPolicyConfig
	Snapshot adminauthorization.Snapshot
}

type ActivateRequest struct {
	ChangeID       string
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type Activation struct {
	Active   storage.ActiveManagementConfig
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
		return nil, errors.New("management configuration storage is required")
	}
	return &Service{store: store, now: time.Now, newID: uuid.NewString}, nil
}

func (service *Service) CurrentPolicy(ctx context.Context) (PolicyState, error) {
	active, err := service.store.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID)
	if errors.Is(err, storage.ErrNotFound) {
		return PolicyState{Snapshot: adminauthorization.Snapshot{
			Version: adminauthorization.CurrentVersion, Roles: []adminauthorization.RoleDefinition{}, Bindings: []adminauthorization.Binding{},
		}}, nil
	}
	if err != nil {
		return PolicyState{}, errors.Join(ErrPolicyUnavailable, errors.New("read active policy"))
	}
	config, err := service.store.AdminPolicyConfigs().Get(ctx, active.ObjectID)
	if err != nil || config.ValidationState != storage.ConfigValidationValid || config.SpecHash != policySpecHash(config.Spec) {
		return PolicyState{}, errors.Join(ErrPolicyUnavailable, errors.New("verify active policy configuration"))
	}
	snapshot, err := decodePolicySpec(config.Spec)
	if err != nil {
		return PolicyState{}, err
	}
	return PolicyState{Active: true, Pointer: active, Config: config, Snapshot: snapshot}, nil
}

func (service *Service) CreatePolicyDraft(ctx context.Context, request PolicyDraftRequest) (PolicyDraft, error) {
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return PolicyDraft{}, err
	}
	request.RequestID, request.Reason = strings.TrimSpace(request.RequestID), strings.TrimSpace(request.Reason)
	if request.RequestID == "" || request.Reason == "" {
		return PolicyDraft{}, ErrInvalidRequest
	}
	snapshot := request.Snapshot
	if snapshot.Version == 0 {
		snapshot.Version = adminauthorization.CurrentVersion
	}
	if _, err := adminauthorization.New(snapshot); err != nil {
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
		Spec   json.RawMessage `json:"spec"`
		Reason string          `json:"reason"`
	}{Spec: spec, Reason: request.Reason})
	now := service.now().UTC()
	configID, changeID, auditID := service.newID(), service.newID(), service.newID()
	result := PolicyDraft{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash {
				return storage.ErrIdempotencyMismatch
			}
			config, getErr := repositories.AdminPolicyConfigs().Get(ctx, existing.ProposedObjectID)
			if getErr != nil {
				return getErr
			}
			result = PolicyDraft{Config: config, Change: existing, Replayed: true}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		baseObjectID, baseErr := currentPolicyObjectID(ctx, repositories)
		if baseErr != nil {
			return baseErr
		}
		config, createErr := repositories.AdminPolicyConfigs().Create(ctx, storage.AdminPolicyConfig{
			ID: configID, Spec: spec, ValidationState: storage.ConfigValidationValid,
			Validation: json.RawMessage(`{"valid":true}`), CreatedBy: actorID,
			CreatedAuthenticationType: authenticationType, Reason: request.Reason, CreatedAt: now,
		})
		if createErr != nil {
			return createErr
		}
		if err := persistAuthorizationDefinitions(ctx, repositories.AuthorizationDefinitions(), config.ID, snapshot, actorID); err != nil {
			return err
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationPolicy, ConfigurationID: storage.ManagementPolicyID,
			BaseObjectID: baseObjectID, ProposedObjectID: config.ID, Status: storage.ChangeStatusValidated,
			IdempotencyHash: idempotencyHash[:], RequestHash: requestHash, RequestedBy: actorID,
			RequestedAuthenticationType: authenticationType, Reason: request.Reason,
			Validation: json.RawMessage(`{"valid":true}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.policy.config.create", ResourceType: "admin-policy",
			ResourceID: config.ID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "objectId": config.ID, "baseObjectId": baseObjectID,
				"idempotencyKeyHash": hex.EncodeToString(idempotencyHash[:]),
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = PolicyDraft{Config: config, Change: change}
		return nil
	})
	if err != nil {
		return PolicyDraft{}, mapServiceError(err)
	}
	return result, nil
}

func persistAuthorizationDefinitions(ctx context.Context, repository storage.AuthorizationDefinitionRepository, policyID string, snapshot adminauthorization.Snapshot, actorID string) error {
	roles := append(adminauthorization.BuiltInRoleDefinitions(), snapshot.Roles...)
	for _, role := range roles {
		definition, err := json.Marshal(role)
		if err != nil {
			return err
		}
		if err := repository.CreateRole(ctx, storage.AuthorizationRoleRecord{PolicyID: policyID, ID: string(role.ID), Definition: definition}); err != nil {
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
			PolicyID: policyID, ID: binding.ID, RoleID: string(binding.RoleID), SubjectType: string(binding.Subject.Type),
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
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return Activation{}, err
	}
	request.ChangeID, request.RequestID, request.Reason = strings.TrimSpace(request.ChangeID), strings.TrimSpace(request.RequestID), strings.TrimSpace(request.Reason)
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
		if change.ConfigurationType != storage.ManagementConfigurationPolicy || !bytes.Equal(change.IdempotencyHash, idempotencyHash[:]) {
			return storage.ErrIdempotencyMismatch
		}
		if change.Status == storage.ChangeStatusPublished {
			active, getErr := repositories.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID)
			if getErr != nil || active.ObjectID != change.ProposedObjectID {
				return storage.ErrConflict
			}
			result = Activation{Active: active, ChangeID: change.ID, Replayed: true}
			return nil
		}
		if change.Status != storage.ChangeStatusValidated {
			return storage.ErrConflict
		}
		proposed, err := repositories.AdminPolicyConfigs().Get(ctx, change.ProposedObjectID)
		if err != nil || !configCanBePublished(proposed, actorID, authenticationType) {
			return storage.ErrConflict
		}
		active, err := repositories.ActiveManagementConfigs().Set(ctx, storage.ManagementConfigurationPolicy,
			storage.ManagementPolicyID, change.ProposedObjectID, actorID, authenticationType, now)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(ctx, change.ID, storage.ChangeStatusValidated, storage.ChangeStatusPublished, change.Validation, now); err != nil {
			return err
		}
		if _, err := repositories.ManagementState().RetireBootstrap(ctx, now); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.policy.publish", ResourceType: "admin-policy",
			ResourceID: change.ID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "objectId": active.ObjectID, "previousObjectId": change.BaseObjectID,
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

func currentPolicyObjectID(ctx context.Context, repositories storage.Repositories) (string, error) {
	active, err := repositories.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID)
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return active.ObjectID, nil
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
	values["authenticationType"], values["reason"] = authenticationType, reason
	encoded, _ := json.Marshal(values)
	return encoded
}

func configHasPlatformAdmin(config storage.AdminPolicyConfig) bool {
	snapshot, err := decodePolicySpec(config.Spec)
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

func configCanBePublished(config storage.AdminPolicyConfig, actorID, authentication string) bool {
	if !configHasPlatformAdmin(config) {
		return false
	}
	if authentication == string(adminauthorization.AuthenticationBreakGlass) {
		return true
	}
	snapshot, err := decodePolicySpec(config.Spec)
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

package managementconfig

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type DelegationRequest struct {
	Binding        *adminauthorization.Binding
	DeleteID       string
	Namespace      string
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

// ApplyDelegation creates and activates a new immutable authorization policy.
// The caller must separately prove namespace.authorization.delegate for the
// exact Namespace; this method constrains the mutation so that proof cannot be
// reused for another Namespace or a platform-managed binding.
func (service *Service) ApplyDelegation(ctx context.Context, request DelegationRequest) (Activation, error) {
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return Activation{}, err
	}
	request.Namespace, request.DeleteID = strings.TrimSpace(request.Namespace), strings.TrimSpace(request.DeleteID)
	request.Reason, request.RequestID = strings.TrimSpace(request.Reason), strings.TrimSpace(request.RequestID)
	if request.Namespace == "" || request.Reason == "" || request.RequestID == "" ||
		(request.Binding == nil) == (request.DeleteID == "") {
		return Activation{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return Activation{}, err
	}
	requestHash := hashRequest(struct {
		Binding   *adminauthorization.Binding `json:"binding,omitempty"`
		DeleteID  string                      `json:"deleteId,omitempty"`
		Namespace string                      `json:"namespace"`
		Reason    string                      `json:"reason"`
	}{request.Binding, request.DeleteID, request.Namespace, request.Reason})
	now := service.now().UTC()
	configID, changeID, auditID := service.newID(), service.newID(), service.newID()
	result := Activation{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationPolicy,
			storage.ManagementPolicyID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash || existing.Status != storage.ChangeStatusPublished {
				return storage.ErrIdempotencyMismatch
			}
			active, getErr := repositories.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID)
			if getErr != nil || active.ObjectID != existing.ProposedObjectID {
				return storage.ErrConflict
			}
			result = Activation{Active: active, ChangeID: existing.ID, Replayed: true}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		active, err := repositories.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID)
		if err != nil {
			return storage.ErrConflict
		}
		current, err := repositories.AdminPolicyConfigs().Get(ctx, active.ObjectID)
		if err != nil {
			return err
		}
		snapshot, err := decodePolicySpec(current.Spec)
		if err != nil {
			return err
		}
		nextBindings := append([]adminauthorization.Binding(nil), snapshot.Bindings...)
		if request.Binding != nil {
			candidate := *request.Binding
			candidate.ID = strings.TrimSpace(candidate.ID)
			candidate.ManagedBy, candidate.CreatedBy = adminauthorization.ManagedByDelegated, actorID
			candidate.Scope = adminauthorization.BindingScope{Type: adminauthorization.ScopeNamespaces, Names: []string{request.Namespace}}
			replaced := false
			for index, existing := range nextBindings {
				if existing.ID != candidate.ID {
					continue
				}
				if !delegationOwnsNamespace(existing, request.Namespace) {
					return storage.ErrConflict
				}
				nextBindings[index], replaced = candidate, true
			}
			if !replaced {
				nextBindings = append(nextBindings, candidate)
			}
		} else {
			found := false
			filtered := nextBindings[:0]
			for _, existing := range nextBindings {
				if existing.ID == request.DeleteID {
					if !delegationOwnsNamespace(existing, request.Namespace) {
						return storage.ErrConflict
					}
					found = true
					continue
				}
				filtered = append(filtered, existing)
			}
			if !found {
				return storage.ErrNotFound
			}
			nextBindings = filtered
		}
		snapshot.Bindings = nextBindings
		if _, err := adminauthorization.New(snapshot); err != nil {
			return ErrInvalidRequest
		}
		spec, err := json.Marshal(struct {
			Version  int                                 `json:"version"`
			Roles    []adminauthorization.RoleDefinition `json:"roles,omitempty"`
			Bindings []adminauthorization.Binding        `json:"bindings"`
		}{snapshot.Version, snapshot.Roles, snapshot.Bindings})
		if err != nil {
			return err
		}
		config, err := repositories.AdminPolicyConfigs().Create(ctx, storage.AdminPolicyConfig{
			ID: configID, Spec: spec, ValidationState: storage.ConfigValidationValid,
			Validation: json.RawMessage(`{"valid":true,"operation":"delegation"}`), CreatedBy: actorID,
			CreatedAuthenticationType: authenticationType, Reason: request.Reason, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := persistAuthorizationDefinitions(ctx, repositories.AuthorizationDefinitions(), config.ID, snapshot, actorID); err != nil {
			return err
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationPolicy,
			ConfigurationID: storage.ManagementPolicyID, BaseObjectID: active.ObjectID,
			ProposedObjectID: config.ID,
			Status:           storage.ChangeStatusValidated, IdempotencyHash: idempotencyHash[:], RequestHash: requestHash,
			RequestedBy: actorID, RequestedAuthenticationType: authenticationType,
			Reason: request.Reason, Validation: json.RawMessage(`{"valid":true,"operation":"delegation"}`),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		next, err := repositories.ActiveManagementConfigs().Set(ctx, storage.ManagementConfigurationPolicy,
			storage.ManagementPolicyID, config.ID, actorID, authenticationType, now)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(ctx, change.ID, storage.ChangeStatusValidated,
			storage.ChangeStatusPublished, change.Validation, now); err != nil {
			return err
		}
		action, bindingID := "admin.authorization.delegation.upsert", request.DeleteID
		if request.Binding != nil {
			bindingID = request.Binding.ID
		} else {
			action = "admin.authorization.delegation.delete"
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: action, ResourceType: "authorization-binding",
			ResourceID: bindingID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"namespace": request.Namespace, "previousObjectId": active.ObjectID, "objectId": next.ObjectID, "changeId": change.ID,
				"idempotencyKeyHash": hex.EncodeToString(idempotencyHash[:]),
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = Activation{Active: next, ChangeID: change.ID}
		return nil
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Activation{}, err
		}
		return Activation{}, mapServiceError(err)
	}
	return result, nil
}

func delegationOwnsNamespace(binding adminauthorization.Binding, namespace string) bool {
	return binding.ManagedBy == adminauthorization.ManagedByDelegated && binding.Scope.Type == adminauthorization.ScopeNamespaces &&
		len(binding.Scope.Names) == 1 && binding.Scope.Names[0] == namespace && len(binding.Scope.LabelSelectors) == 0
}

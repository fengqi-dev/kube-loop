package managementconfig

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

var providerIdentifier = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)
var ErrProviderUnavailable = errors.New("management Provider is unavailable")

type ProviderCandidate struct {
	ID     string
	Type   string
	Config json.RawMessage
}

type ProviderValidator interface {
	Validate(context.Context, ProviderCandidate) (json.RawMessage, error)
}

type ProviderActivator interface {
	Prepare(context.Context, ProviderCandidate) (func(), error)
}

type ProviderDraftRequest struct {
	Candidate      ProviderCandidate
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type ProviderDraft struct {
	Config   storage.ProviderConfig
	Change   storage.ConfigChangeRequest
	Replayed bool
}

type ProviderActivateRequest struct {
	ProviderID     string
	ChangeID       string
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type ProviderState struct {
	Active  bool
	Pointer storage.ActiveManagementConfig
	Config  storage.ProviderConfig
}

type ProviderService struct {
	store     Store
	validator ProviderValidator
	activator ProviderActivator
	now       func() time.Time
	newID     func() string
}

func NewProviderService(store Store, validator ProviderValidator, activator ProviderActivator) (*ProviderService, error) {
	if store == nil || validator == nil || activator == nil {
		return nil, errors.New("management Provider service dependencies are required")
	}
	return &ProviderService{store: store, validator: validator, activator: activator, now: time.Now, newID: uuid.NewString}, nil
}

func (service *ProviderService) Validate(ctx context.Context, candidate ProviderCandidate) (json.RawMessage, error) {
	candidate, err := service.prepareCandidate(ctx, candidate)
	if err != nil {
		return nil, err
	}
	validation, err := service.validator.Validate(ctx, candidate)
	if err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	if len(validation) == 0 {
		validation = json.RawMessage(`{"valid":true}`)
	}
	return append(json.RawMessage(nil), validation...), nil
}

func (service *ProviderService) ListCurrent(ctx context.Context) ([]ProviderState, error) {
	pointers, err := service.store.ActiveManagementConfigs().List(ctx, storage.ManagementConfigurationProvider)
	if err != nil {
		return nil, fmt.Errorf("list active Providers: %w", err)
	}
	states := make([]ProviderState, 0, len(pointers))
	for _, pointer := range pointers {
		state, err := service.Current(ctx, pointer.ConfigurationID)
		if err != nil {
			return nil, err
		}
		if state.Active {
			states = append(states, state)
		}
	}
	return states, nil
}

func (service *ProviderService) Current(ctx context.Context, providerID string) (ProviderState, error) {
	providerID = strings.TrimSpace(providerID)
	if !providerIdentifier.MatchString(providerID) {
		return ProviderState{}, ErrInvalidRequest
	}
	active, err := service.store.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationProvider, providerID)
	if errors.Is(err, storage.ErrNotFound) {
		return ProviderState{}, nil
	}
	if err != nil {
		return ProviderState{}, fmt.Errorf("read active Provider: %w", err)
	}
	config, err := service.store.ProviderConfigs().Get(ctx, active.ObjectID)
	if err != nil || config.ProviderID != providerID || config.ValidationState != storage.ConfigValidationValid || config.ConfigHash != providerConfigHash(config.Config) {
		return ProviderState{}, ErrProviderUnavailable
	}
	return ProviderState{Active: true, Pointer: active, Config: config}, nil
}

func (service *ProviderService) CreateDraft(ctx context.Context, request ProviderDraftRequest) (ProviderDraft, error) {
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil {
		return ProviderDraft{}, err
	}
	request.RequestID, request.Reason = strings.TrimSpace(request.RequestID), strings.TrimSpace(request.Reason)
	candidate, err := service.prepareCandidate(ctx, request.Candidate)
	if err != nil || request.RequestID == "" || request.Reason == "" {
		return ProviderDraft{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return ProviderDraft{}, err
	}
	requestHash := hashRequest(struct {
		Candidate ProviderCandidate `json:"candidate"`
		Reason    string            `json:"reason"`
	}{candidate, request.Reason})
	existing, lookupErr := service.store.ConfigChangeRequests().GetByIdempotencyHash(
		ctx, actorID, authenticationType, storage.ManagementConfigurationProvider, candidate.ID, idempotencyHash[:],
	)
	if lookupErr == nil {
		if existing.RequestHash != requestHash {
			return ProviderDraft{}, errors.Join(ErrConflict, storage.ErrIdempotencyMismatch)
		}
		config, getErr := service.store.ProviderConfigs().Get(ctx, existing.ProposedObjectID)
		if getErr != nil {
			return ProviderDraft{}, getErr
		}
		return ProviderDraft{Config: config, Change: existing, Replayed: true}, nil
	}
	if !errors.Is(lookupErr, storage.ErrNotFound) {
		return ProviderDraft{}, lookupErr
	}
	validation, err := service.Validate(ctx, candidate)
	if err != nil {
		return ProviderDraft{}, err
	}
	now := service.now().UTC()
	configID, changeID, auditID := service.newID(), service.newID(), service.newID()
	result := ProviderDraft{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationProvider, candidate.ID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash {
				return storage.ErrIdempotencyMismatch
			}
			config, getErr := repositories.ProviderConfigs().Get(ctx, existing.ProposedObjectID)
			if getErr != nil {
				return getErr
			}
			result = ProviderDraft{Config: config, Change: existing, Replayed: true}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		baseObjectID, baseErr := currentProviderObjectID(ctx, repositories, candidate.ID)
		if baseErr != nil {
			return baseErr
		}
		config, createErr := repositories.ProviderConfigs().Create(ctx, storage.ProviderConfig{
			ID: configID, ProviderID: candidate.ID, ProviderType: candidate.Type, Config: candidate.Config,
			SecretAliases: json.RawMessage(`{}`), ValidationState: storage.ConfigValidationValid, Validation: validation,
			CreatedBy: actorID, CreatedAuthenticationType: authenticationType, Reason: request.Reason, CreatedAt: now,
		})
		if createErr != nil {
			return createErr
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationProvider, ConfigurationID: candidate.ID,
			BaseObjectID: baseObjectID, ProposedObjectID: config.ID, Status: storage.ChangeStatusValidated,
			IdempotencyHash: idempotencyHash[:], RequestHash: requestHash, RequestedBy: actorID,
			RequestedAuthenticationType: authenticationType, Reason: request.Reason,
			Validation: validation, CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.provider.config.create", ResourceType: "auth-provider",
			ResourceID: candidate.ID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "objectId": config.ID, "baseObjectId": baseObjectID, "providerType": candidate.Type,
				"idempotencyKeyHash": hex.EncodeToString(idempotencyHash[:]),
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = ProviderDraft{Config: config, Change: change}
		return nil
	})
	if err != nil {
		return ProviderDraft{}, mapServiceError(err)
	}
	return result, nil
}

func (service *ProviderService) Publish(ctx context.Context, request ProviderActivateRequest) (Activation, error) {
	request.ProviderID, request.ChangeID = strings.TrimSpace(request.ProviderID), strings.TrimSpace(request.ChangeID)
	request.RequestID, request.Reason = strings.TrimSpace(request.RequestID), strings.TrimSpace(request.Reason)
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil || !providerIdentifier.MatchString(request.ProviderID) || request.ChangeID == "" || request.RequestID == "" || request.Reason == "" {
		return Activation{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return Activation{}, err
	}
	change, err := service.store.ConfigChangeRequests().GetByID(ctx, request.ChangeID)
	if err != nil {
		return Activation{}, err
	}
	if change.ConfigurationType != storage.ManagementConfigurationProvider || change.ConfigurationID != request.ProviderID || !bytes.Equal(change.IdempotencyHash, idempotencyHash[:]) {
		return Activation{}, ErrConflict
	}
	config, err := service.store.ProviderConfigs().Get(ctx, change.ProposedObjectID)
	if err != nil || config.ProviderID != request.ProviderID {
		return Activation{}, ErrConflict
	}
	install, err := service.activator.Prepare(ctx, providerCandidate(config))
	if err != nil || install == nil {
		return Activation{}, errors.Join(ErrInvalidRequest, err)
	}
	now, auditID := service.now().UTC(), service.newID()
	result := Activation{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		fresh, err := repositories.ConfigChangeRequests().GetByID(ctx, request.ChangeID)
		if err != nil {
			return err
		}
		if fresh.ConfigurationType != storage.ManagementConfigurationProvider || fresh.ConfigurationID != request.ProviderID || !bytes.Equal(fresh.IdempotencyHash, idempotencyHash[:]) {
			return storage.ErrConflict
		}
		if fresh.Status == storage.ChangeStatusPublished {
			active, getErr := repositories.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationProvider, request.ProviderID)
			if getErr != nil || active.ObjectID != fresh.ProposedObjectID {
				return storage.ErrConflict
			}
			result = Activation{Active: active, ChangeID: fresh.ID, Replayed: true}
			return nil
		}
		if fresh.Status != storage.ChangeStatusValidated {
			return storage.ErrConflict
		}
		active, err := repositories.ActiveManagementConfigs().Set(ctx, storage.ManagementConfigurationProvider,
			request.ProviderID, fresh.ProposedObjectID, actorID, authenticationType, now)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(ctx, fresh.ID, storage.ChangeStatusValidated, storage.ChangeStatusPublished, fresh.Validation, now); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.provider.publish", ResourceType: "auth-provider",
			ResourceID: request.ProviderID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": fresh.ID, "objectId": active.ObjectID, "previousObjectId": fresh.BaseObjectID,
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = Activation{Active: active, ChangeID: fresh.ID}
		return nil
	})
	if err != nil {
		return Activation{}, mapServiceError(err)
	}
	install()
	return result, nil
}

func normalizeProviderCandidate(candidate ProviderCandidate) (ProviderCandidate, error) {
	candidate.ID, candidate.Type = strings.TrimSpace(candidate.ID), strings.ToLower(strings.TrimSpace(candidate.Type))
	if !providerIdentifier.MatchString(candidate.ID) || candidate.Type != "oidc" {
		return ProviderCandidate{}, ErrInvalidRequest
	}
	var err error
	if candidate.Config, err = canonicalProviderObject(candidate.Config); err != nil {
		return ProviderCandidate{}, err
	}
	return candidate, nil
}

func (service *ProviderService) prepareCandidate(ctx context.Context, candidate ProviderCandidate) (ProviderCandidate, error) {
	candidate, err := normalizeProviderCandidate(candidate)
	if err != nil {
		return ProviderCandidate{}, err
	}
	var next map[string]json.RawMessage
	if json.Unmarshal(candidate.Config, &next) != nil {
		return ProviderCandidate{}, ErrInvalidRequest
	}
	if secretConfigured(next["clientSecret"]) {
		return candidate, nil
	}
	active, err := service.store.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationProvider, candidate.ID)
	if errors.Is(err, storage.ErrNotFound) {
		return candidate, nil
	}
	if err != nil {
		return ProviderCandidate{}, err
	}
	current, err := service.store.ProviderConfigs().Get(ctx, active.ObjectID)
	if err != nil || current.ProviderID != candidate.ID {
		return ProviderCandidate{}, ErrProviderUnavailable
	}
	var previous map[string]json.RawMessage
	if json.Unmarshal(current.Config, &previous) != nil || !secretConfigured(previous["clientSecret"]) {
		return ProviderCandidate{}, ErrInvalidRequest
	}
	next["clientSecret"] = append(json.RawMessage(nil), previous["clientSecret"]...)
	candidate.Config, err = json.Marshal(next)
	if err != nil {
		return ProviderCandidate{}, ErrInvalidRequest
	}
	return candidate, nil
}

func currentProviderObjectID(ctx context.Context, repositories storage.Repositories, providerID string) (string, error) {
	active, err := repositories.ActiveManagementConfigs().Get(ctx, storage.ManagementConfigurationProvider, providerID)
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return active.ObjectID, nil
}

func secretConfigured(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func canonicalProviderObject(value json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidRequest
	}
	return json.Marshal(object)
}

func providerCandidate(config storage.ProviderConfig) ProviderCandidate {
	return ProviderCandidate{ID: config.ProviderID, Type: config.ProviderType, Config: append(json.RawMessage(nil), config.Config...)}
}

func providerConfigHash(value json.RawMessage) string { return hashRequest(json.RawMessage(value)) }

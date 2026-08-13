package revision

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
	// Prepare validates connectivity and returns an infallible atomic runtime
	// install closure. It must not mutate the live
	// Registry before that closure is invoked after the database commit.
	Prepare(context.Context, ProviderCandidate) (func(), error)
}

type ProviderDraftRequest struct {
	Candidate      ProviderCandidate
	ExpectedETag   uint64
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type ProviderDraft struct {
	Revision storage.ProviderConfigRevision
	Change   storage.ConfigChangeRequest
	Replayed bool
}

type ProviderActivateRequest struct {
	ProviderID     string
	ChangeID       string
	ExpectedETag   uint64
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type ProviderRollbackRequest struct {
	ProviderID     string
	TargetRevision uint64
	ExpectedETag   uint64
	IdempotencyKey string
	Reason         string
	RequestID      string
	Actor          Actor
}

type ProviderState struct {
	Active   bool
	Pointer  storage.ActiveManagementRevision
	Revision storage.ProviderConfigRevision
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
	pointers, err := service.store.ActiveManagementRevisions().List(ctx, storage.ManagementConfigurationProvider)
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

func (service *ProviderService) Current(ctx context.Context, providerID string) (ProviderState, error) {
	providerID = strings.TrimSpace(providerID)
	if !providerIdentifier.MatchString(providerID) {
		return ProviderState{}, ErrInvalidRequest
	}
	active, err := service.store.ActiveManagementRevisions().Get(ctx, storage.ManagementConfigurationProvider, providerID)
	if errors.Is(err, storage.ErrNotFound) {
		return ProviderState{}, nil
	}
	if err != nil {
		return ProviderState{}, fmt.Errorf("read active Provider: %w", err)
	}
	revision, err := service.store.ProviderConfigRevisions().Get(ctx, active.Revision)
	if err != nil || revision.ProviderID != providerID || revision.ValidationState != storage.RevisionValidationValid ||
		revision.ConfigHash != providerConfigHash(revision.Config) {
		return ProviderState{}, ErrProviderUnavailable
	}
	return ProviderState{Active: true, Pointer: active, Revision: revision}, nil
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
		Candidate    ProviderCandidate `json:"candidate"`
		ExpectedETag uint64            `json:"expectedEtag"`
		Reason       string            `json:"reason"`
	}{candidate, request.ExpectedETag, request.Reason})
	existing, lookupErr := service.store.ConfigChangeRequests().GetByIdempotencyHash(
		ctx, actorID, authenticationType, storage.ManagementConfigurationProvider, candidate.ID, idempotencyHash[:],
	)
	if lookupErr == nil {
		if existing.RequestHash != requestHash {
			return ProviderDraft{}, errors.Join(ErrConflict, storage.ErrIdempotencyMismatch)
		}
		revision, getErr := service.store.ProviderConfigRevisions().Get(ctx, existing.ProposedRevision)
		if getErr != nil {
			return ProviderDraft{}, getErr
		}
		return ProviderDraft{Revision: revision, Change: existing, Replayed: true}, nil
	}
	if !errors.Is(lookupErr, storage.ErrNotFound) {
		return ProviderDraft{}, lookupErr
	}
	validation, err := service.Validate(ctx, candidate)
	if err != nil {
		return ProviderDraft{}, err
	}
	now := service.now().UTC()
	revisionID, changeID, auditID := service.newID(), service.newID(), service.newID()
	result := ProviderDraft{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationProvider, candidate.ID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash {
				return storage.ErrIdempotencyMismatch
			}
			revision, getErr := repositories.ProviderConfigRevisions().Get(ctx, existing.ProposedRevision)
			if getErr != nil {
				return getErr
			}
			result = ProviderDraft{Revision: revision, Change: existing, Replayed: true}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		baseRevision, baseErr := currentProviderBase(ctx, repositories, candidate.ID, request.ExpectedETag)
		if baseErr != nil {
			return baseErr
		}
		revision, createErr := repositories.ProviderConfigRevisions().Create(ctx, storage.ProviderConfigRevision{
			ID: revisionID, ProviderID: candidate.ID, ProviderType: candidate.Type,
			Config: candidate.Config, SecretAliases: json.RawMessage(`{}`),
			ValidationState: storage.RevisionValidationValid, Validation: validation,
			CreatedBy: actorID, CreatedAuthenticationType: authenticationType, Reason: request.Reason, CreatedAt: now,
		})
		if createErr != nil {
			return createErr
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationProvider, ConfigurationID: candidate.ID,
			BaseRevision: baseRevision, BaseETag: request.ExpectedETag, ProposedRevision: revision.Revision,
			Status: storage.ChangeStatusValidated, IdempotencyHash: idempotencyHash[:], RequestHash: requestHash,
			RequestedBy: actorID, RequestedAuthenticationType: authenticationType, Reason: request.Reason,
			Validation: validation, CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.provider.revision.create",
			ResourceType: "auth-provider", ResourceID: candidate.ID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "newRevision": revision.Revision, "baseRevision": baseRevision,
				"baseEtag": request.ExpectedETag, "providerType": candidate.Type,
				"idempotencyKeyHash": hex.EncodeToString(idempotencyHash[:]),
			}), CreatedAt: now,
		}); err != nil {
			return err
		}
		result = ProviderDraft{Revision: revision, Change: change}
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
	if err != nil || !providerIdentifier.MatchString(request.ProviderID) || request.ChangeID == "" ||
		request.RequestID == "" || request.Reason == "" {
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
	if change.ConfigurationType != storage.ManagementConfigurationProvider || change.ConfigurationID != request.ProviderID ||
		change.BaseETag != request.ExpectedETag || !bytes.Equal(change.IdempotencyHash, idempotencyHash[:]) {
		return Activation{}, ErrConflict
	}
	revision, err := service.store.ProviderConfigRevisions().Get(ctx, change.ProposedRevision)
	if err != nil || revision.ProviderID != request.ProviderID {
		return Activation{}, ErrConflict
	}
	install, err := service.activator.Prepare(ctx, providerCandidate(revision))
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
		if fresh.ConfigurationType != storage.ManagementConfigurationProvider || fresh.ConfigurationID != request.ProviderID ||
			fresh.BaseETag != request.ExpectedETag || !bytes.Equal(fresh.IdempotencyHash, idempotencyHash[:]) {
			return storage.ErrConflict
		}
		if fresh.Status == storage.ChangeStatusPublished {
			active, getErr := repositories.ActiveManagementRevisions().Get(ctx, storage.ManagementConfigurationProvider, request.ProviderID)
			if getErr != nil || active.Revision != fresh.ProposedRevision || active.ETag != fresh.BaseETag+1 {
				return storage.ErrConflict
			}
			result = Activation{Active: active, ChangeID: fresh.ID, Replayed: true}
			return nil
		}
		if fresh.Status != storage.ChangeStatusValidated {
			return storage.ErrConflict
		}
		active, err := repositories.ActiveManagementRevisions().CompareAndSwap(
			ctx, storage.ManagementConfigurationProvider, request.ProviderID, fresh.ProposedRevision,
			request.ExpectedETag, actorID, authenticationType, now,
		)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(ctx, fresh.ID, storage.ChangeStatusValidated,
			storage.ChangeStatusPublished, fresh.Validation, now); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.provider.publish", ResourceType: "auth-provider",
			ResourceID: request.ProviderID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": fresh.ID, "oldRevision": fresh.BaseRevision, "newRevision": active.Revision,
				"oldEtag": request.ExpectedETag, "newEtag": active.ETag,
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

func (service *ProviderService) Rollback(ctx context.Context, request ProviderRollbackRequest) (Activation, error) {
	request.ProviderID, request.RequestID = strings.TrimSpace(request.ProviderID), strings.TrimSpace(request.RequestID)
	request.Reason = strings.TrimSpace(request.Reason)
	actorID, authenticationType, principalID, err := normalizeActor(request.Actor)
	if err != nil || !providerIdentifier.MatchString(request.ProviderID) || request.TargetRevision == 0 ||
		request.ExpectedETag == 0 || request.RequestID == "" || request.Reason == "" {
		return Activation{}, ErrInvalidRequest
	}
	idempotencyHash, err := hashIdempotencyKey(request.IdempotencyKey)
	if err != nil {
		return Activation{}, err
	}
	target, err := service.store.ProviderConfigRevisions().Get(ctx, request.TargetRevision)
	if err != nil || target.ProviderID != request.ProviderID || target.ValidationState != storage.RevisionValidationValid {
		return Activation{}, ErrConflict
	}
	install, err := service.activator.Prepare(ctx, providerCandidate(target))
	if err != nil || install == nil {
		return Activation{}, errors.Join(ErrInvalidRequest, err)
	}
	requestHash := hashRequest(struct {
		TargetRevision uint64 `json:"targetRevision"`
		ExpectedETag   uint64 `json:"expectedEtag"`
		Reason         string `json:"reason"`
	}{request.TargetRevision, request.ExpectedETag, request.Reason})
	now, changeID, auditID := service.now().UTC(), service.newID(), service.newID()
	result := Activation{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		existing, lookupErr := repositories.ConfigChangeRequests().GetByIdempotencyHash(
			ctx, actorID, authenticationType, storage.ManagementConfigurationProvider, request.ProviderID, idempotencyHash[:],
		)
		if lookupErr == nil {
			if existing.RequestHash != requestHash || existing.Status != storage.ChangeStatusRolledBack {
				return storage.ErrIdempotencyMismatch
			}
			result = Activation{Active: storage.ActiveManagementRevision{
				ConfigurationType: storage.ManagementConfigurationProvider, ConfigurationID: request.ProviderID,
				Revision: existing.ProposedRevision, ETag: existing.BaseETag + 1,
				UpdatedBy: actorID, UpdatedAuthenticationType: authenticationType, UpdatedAt: existing.UpdatedAt,
			}, ChangeID: existing.ID, Replayed: true}
			return nil
		}
		if !errors.Is(lookupErr, storage.ErrNotFound) {
			return lookupErr
		}
		active, err := repositories.ActiveManagementRevisions().Get(ctx, storage.ManagementConfigurationProvider, request.ProviderID)
		if err != nil {
			return err
		}
		if active.ETag != request.ExpectedETag || active.Revision == request.TargetRevision {
			return storage.ErrConflict
		}
		change := storage.ConfigChangeRequest{
			ID: changeID, ConfigurationType: storage.ManagementConfigurationProvider, ConfigurationID: request.ProviderID,
			BaseRevision: active.Revision, BaseETag: active.ETag, ProposedRevision: request.TargetRevision,
			Status: storage.ChangeStatusValidated, IdempotencyHash: idempotencyHash[:], RequestHash: requestHash,
			RequestedBy: actorID, RequestedAuthenticationType: authenticationType, Reason: request.Reason,
			Validation: json.RawMessage(`{"valid":true,"operation":"rollback"}`), CreatedAt: now, UpdatedAt: now,
		}
		if err := repositories.ConfigChangeRequests().Create(ctx, change); err != nil {
			return err
		}
		next, err := repositories.ActiveManagementRevisions().CompareAndSwap(
			ctx, storage.ManagementConfigurationProvider, request.ProviderID, request.TargetRevision,
			active.ETag, actorID, authenticationType, now,
		)
		if err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(ctx, change.ID, storage.ChangeStatusValidated,
			storage.ChangeStatusPublished, change.Validation, now); err != nil {
			return err
		}
		if err := repositories.ConfigChangeRequests().UpdateStatus(ctx, change.ID, storage.ChangeStatusPublished,
			storage.ChangeStatusRolledBack, change.Validation, now); err != nil {
			return err
		}
		if err := repositories.Audit().Append(ctx, storage.AuditEvent{
			ID: auditID, PrincipalID: principalID, Action: "admin.provider.rollback", ResourceType: "auth-provider",
			ResourceID: request.ProviderID, Outcome: "success", RequestID: request.RequestID,
			Metadata: auditMetadata(authenticationType, request.Reason, map[string]any{
				"changeId": change.ID, "oldRevision": active.Revision, "newRevision": next.Revision,
				"oldEtag": active.ETag, "newEtag": next.ETag,
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
	active, err := service.store.ActiveManagementRevisions().Get(
		ctx, storage.ManagementConfigurationProvider, candidate.ID,
	)
	if errors.Is(err, storage.ErrNotFound) {
		return candidate, nil
	}
	if err != nil {
		return ProviderCandidate{}, err
	}
	current, err := service.store.ProviderConfigRevisions().Get(ctx, active.Revision)
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

func currentProviderBase(ctx context.Context, repositories storage.Repositories, providerID string, expectedETag uint64) (uint64, error) {
	active, err := repositories.ActiveManagementRevisions().Get(ctx, storage.ManagementConfigurationProvider, providerID)
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

func providerCandidate(revision storage.ProviderConfigRevision) ProviderCandidate {
	return ProviderCandidate{ID: revision.ProviderID, Type: revision.ProviderType,
		Config: append(json.RawMessage(nil), revision.Config...)}
}

func providerConfigHash(value json.RawMessage) string { return hashRequest(json.RawMessage(value)) }

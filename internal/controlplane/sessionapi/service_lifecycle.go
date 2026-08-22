package sessionapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

func (handler *Service) create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace string,
) *controlplaneapi.Error {
	request := ctx.Request()
	if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.DeviceID) == "" {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnauthenticated,
			Message: "authenticated device identity is required",
		}
	}
	idempotencyKey, apiError := idempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	now := handler.now().UTC()
	capabilitySnapshot, apiError := handler.capabilities.DiscoverCapabilities(
		request.Context(),
		identity,
		namespace,
	)
	if apiError != nil {
		return apiError
	}
	spec, err := handler.networks.Discover(request.Context(), identity, namespace)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Kubernetes NetworkSpec discovery failed",
			Cause:   err,
		}
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: networkSpecValidationFailedMessage,
			Cause:   err,
		}
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: networkSpecValidationFailedMessage,
			Cause:   err,
		}
	}
	session := storage.Session{
		ID: uuid.NewString(), IdentityID: identity.Subject, DeviceID: identity.DeviceID,
		ClusterID: handler.clusterID, Namespace: namespace, State: sessionStateActive,
		Generation:  1,
		NetworkSpec: specJSON, NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(handler.sessionTTL),
	}
	document := documentWithCapabilities(session, capabilitySnapshot)
	responseJSON, err := json.Marshal(document)
	if err != nil {
		return internalError(err)
	}
	requestHashRaw := sha256.Sum256([]byte("session-create-v1\n" + namespace))
	requestHash := hex.EncodeToString(requestHashRaw[:])
	created := false
	err = handler.storage.WithinTransaction(
		request.Context(),
		func(repositories storage.Repositories) error {
			record, reserved, reserveErr := repositories.Idempotency().
				Reserve(request.Context(), storage.IdempotencyRecord{
					Scope: "session:create:" + identity.Subject, Key: idempotencyKey, RequestHash: requestHash,
					ResourceType: "session", ResourceID: session.ID, Response: responseJSON,
					CreatedAt: now, ExpiresAt: now.Add(handler.maxLifetime),
				})
			if reserveErr != nil {
				return reserveErr
			}
			if !reserved {
				if record.ResourceType != "session" {
					return storage.ErrConflict
				}
				existing, getErr := repositories.Sessions().
					GetByID(request.Context(), record.ResourceID)
				if getErr != nil {
					return getErr
				}
				if !ownedBy(existing, identity, namespace) {
					return storage.ErrNotFound
				}
				session = existing
				return nil
			}
			if createErr := repositories.Sessions().Create(request.Context(), session); createErr != nil {
				return createErr
			}
			created = true
			return nil
		},
	)
	if err != nil {
		return mapStorageError(err)
	}
	if err := handler.registry.Ensure(session.ID); err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Session runtime is unavailable",
			Cause:   err,
		}
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	ctx.Response().
		Header().
		Set("Location", controlplane.APIPathPrefix+"/sessions/"+session.ID+"?namespace="+namespace)
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeDocument(
		ctx,
		map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created],
		documentWithCapabilities(session, capabilitySnapshot),
	)
	return nil
}

func (handler *Service) get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) *controlplaneapi.Error {
	request := ctx.Request()
	session, apiError := handler.loadOwned(request.Context(), identity, namespace, id)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

func (handler *Service) heartbeat(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) *controlplaneapi.Error {
	request := ctx.Request()
	generation, apiError := expectedGeneration(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.loadOwned(request.Context(), identity, namespace, id)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	now := handler.now().UTC()
	if session.State != sessionStateActive || !session.ExpiresAt.After(now) {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session is not active",
		}
	}
	currentCapabilities, capabilityError := handler.capabilities.DiscoverCapabilities(
		request.Context(),
		identity,
		namespace,
	)
	if capabilityError != nil {
		return capabilityError
	}
	if !slices.Contains(currentCapabilities.Capabilities, "cluster.tunnel") {
		if err := handler.storage.Sessions().UpdateState(
			request.Context(),
			session.ID,
			generation,
			"disconnected",
			now,
		); err != nil {
			return mapStorageError(err)
		}
		if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
			return apiError
		}
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "Session access was revoked",
		}
	}
	maximumExpiry := session.CreatedAt.Add(handler.maxLifetime)
	nextExpiry := now.Add(handler.sessionTTL)
	if maximumExpiry.Before(nextExpiry) {
		nextExpiry = maximumExpiry
	}
	if !nextExpiry.After(now) {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session maximum lifetime has elapsed",
		}
	}
	spec, err := handler.networks.Discover(request.Context(), identity, namespace)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Kubernetes NetworkSpec refresh failed",
			Cause:   err,
		}
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: networkSpecValidationFailedMessage,
			Cause:   err,
		}
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: networkSpecValidationFailedMessage,
			Cause:   err,
		}
	}
	if err := handler.storage.Sessions().Heartbeat(
		request.Context(), session.ID, generation, specJSON, specHash, now, nextExpiry,
	); err != nil {
		return mapStorageError(err)
	}
	session, err = handler.storage.Sessions().GetByID(request.Context(), session.ID)
	if err != nil {
		return mapStorageError(err)
	}
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

func (handler *Service) disconnect(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) *controlplaneapi.Error {
	request := ctx.Request()
	generation, apiError := expectedGeneration(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.loadOwned(request.Context(), identity, namespace, id)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	if session.State == "disconnected" || session.State == "expired" {
		if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
			return apiError
		}
		writeDocument(ctx, http.StatusOK, documentFromSession(session))
		return nil
	}
	if err := handler.storage.Sessions().UpdateState(
		request.Context(),
		session.ID,
		generation,
		"disconnected",
		handler.now().UTC(),
	); err != nil {
		return mapStorageError(err)
	}
	if apiError := handler.disconnectRuntime(request.Context(), session.ID); apiError != nil {
		return apiError
	}
	session, err := handler.storage.Sessions().GetByID(request.Context(), session.ID)
	if err != nil {
		return mapStorageError(err)
	}
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

// AttachRuntime is consumed by streamlease without coupling feature handlers

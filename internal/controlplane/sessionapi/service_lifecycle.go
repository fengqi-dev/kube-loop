package sessionapi

import (
	"net/http"
	"slices"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

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

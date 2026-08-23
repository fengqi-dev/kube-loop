package sessionapi

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
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

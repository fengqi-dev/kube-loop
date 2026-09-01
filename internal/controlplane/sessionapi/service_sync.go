package sessionapi

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type trafficBindingListDocument struct {
	Items []trafficbindingclient.SessionBinding `json:"items"`
}

func (handler *Service) listTrafficBindings(
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
	if handler.trafficBindingLister == nil {
		return &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnavailable, Message: "TrafficBinding Sessions are unavailable",
		}
	}
	items, err := handler.trafficBindingLister.List(request.Context(), namespace, session.ID)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "TrafficBinding Session listing failed",
			Cause:   err,
		}
	}
	if items == nil {
		items = []trafficbindingclient.SessionBinding{}
	}
	_ = ctx.JSON(http.StatusOK, trafficBindingListDocument{Items: items})
	return nil
}

func (handler *Service) syncTrafficBindings(
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
	if handler.trafficBindings != nil {
		if err := handler.trafficBindings.Synchronize(
			request.Context(), identity.Subject, session.ID, namespace,
			session.Generation, session.ExpiresAt,
		); err != nil {
			return &controlplaneapi.Error{
				Code:    controlplaneapi.CodeUnavailable,
				Message: "TrafficBinding Session synchronization failed",
				Cause:   err,
			}
		}
	}
	writeDocument(ctx, http.StatusOK, documentFromSession(session))
	return nil
}

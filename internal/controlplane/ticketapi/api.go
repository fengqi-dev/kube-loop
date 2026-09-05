package ticketapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/routequery"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	ticketservice "github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/service"
)

type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Routes struct {
	service  *ticketservice.Service
	sessions SessionValidator
}

func NewRoutes(
	service *ticketservice.Service,
	sessions SessionValidator,
) *Routes {
	return &Routes{service: service, sessions: sessions}
}

func (routes *Routes) Endpoints() controlplane.TicketEndpoints {
	return controlplane.TicketEndpoints{Issue: routes.issue}
}

func (routes *Routes) issue(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
) *controlplaneapi.Error {
	request := ctx.Request()
	sessionID := request.PathValue("sessionID")
	if _, err := uuid.Parse(sessionID); err != nil {
		return controlplaneapi.NotFound()
	}
	namespace, apiError := routequery.Namespace(request)
	if apiError != nil {
		return apiError
	}
	var body issueRequest
	if err := ctx.Bind(&body); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	session, apiError := routes.sessions.RequireActive(
		request.Context(),
		identity,
		namespace,
		sessionID,
	)
	if apiError != nil {
		return apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	ticket, err := routes.service.Issue(
		request.Context(),
		ticketservice.IssueInput{
			IdentityID:       identity.Subject,
			Groups:           append([]string(nil), identity.Groups...),
			DeviceID:         identity.DeviceID,
			SessionID:        session.ID,
			Generation:       session.Generation,
			Namespace:        session.Namespace,
			NetworkSpecHash:  session.NetworkSpecHash,
			SessionExpiresAt: session.ExpiresAt,
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, ticketservice.ErrSessionExpiresSoon):
			return &controlplaneapi.Error{
				Code:    controlplaneapi.CodeConflict,
				Message: ticketservice.ErrSessionExpiresSoon.Error(),
				Cause:   err,
			}
		case errors.Is(err, ticketservice.ErrNoReadyDataPlane):
			return &controlplaneapi.Error{
				Code:    controlplaneapi.CodeUnavailable,
				Message: "No ready Data Plane is available",
				Cause:   err,
			}
		default:
			return &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInternal,
				Message: "RelayTicket issuance failed",
				Cause:   err,
			}
		}
	}
	if err := ctx.JSON(http.StatusCreated, issueResponse{
		TokenType: ticket.TokenType, Ticket: ticket.Value, ExpiresAt: ticket.ExpiresAt,
		DeviceID: ticket.DeviceID, RelayID: ticket.RelayID, Endpoint: ticket.Endpoint,
		TrafficEncryption: ticket.TrafficEncryption,
		NoisePublicKey:    ticket.NoisePublicKey,
	}); err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: "RelayTicket response failed",
			Cause:   err,
		}
	}
	return nil
}

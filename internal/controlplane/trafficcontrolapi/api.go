package trafficcontrolapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

type Coordinator interface {
	Claim(
		context.Context,
		string,
		trafficcontrol.ClaimRequest,
	) (trafficcontrol.ClaimResponse, *controlplaneapi.Error)
	Prepare(
		context.Context,
		string,
		trafficcontrol.PrepareRequest,
	) (trafficcontrol.PrepareResponse, *controlplaneapi.Error)
	Heartbeat(
		context.Context,
		string,
		trafficcontrol.HeartbeatRequest,
	) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error)
	Finish(
		context.Context,
		string,
		trafficcontrol.FinishRequest,
	) (trafficcontrol.FinishResponse, *controlplaneapi.Error)
}

type API struct {
	authenticator relayregistry.Authenticator
	coordinator   Coordinator
}

func New(
	authenticator relayregistry.Authenticator,
	coordinator Coordinator,
) (*API, error) {
	if authenticator == nil || coordinator == nil {
		return nil, errors.New(
			"traffic control authenticator and coordinator are required",
		)
	}
	return &API{authenticator: authenticator, coordinator: coordinator}, nil
}

func (api *API) RegisterRoutes(router *echo.Echo) {
	group := router.Group(trafficcontrol.InternalPathPrefix)
	group.POST("/claim", api.claim)
	group.POST("/prepare", api.prepare)
	group.PUT("/heartbeat", api.heartbeat)
	group.POST("/finish", api.finish)
}

func (api *API) claim(ctx *echo.Context) error {
	relayID, ok := api.authenticate(ctx)
	if !ok {
		return nil
	}
	var request trafficcontrol.ClaimRequest
	if !api.bind(ctx, &request) || request.Validate() != nil {
		return api.writeError(ctx, invalid("traffic claim request is invalid"))
	}
	response, apiError := api.coordinator.Claim(
		ctx.Request().Context(),
		relayID,
		request,
	)
	if apiError != nil {
		return api.writeError(ctx, apiError)
	}
	return ctx.JSON(http.StatusOK, response)
}

func (api *API) prepare(ctx *echo.Context) error {
	relayID, ok := api.authenticate(ctx)
	if !ok {
		return nil
	}
	var request trafficcontrol.PrepareRequest
	if !api.bind(ctx, &request) || request.Validate() != nil ||
		request.RelayID != relayID {
		return api.writeError(
			ctx,
			invalid("traffic prepare request is invalid"),
		)
	}
	response, apiError := api.coordinator.Prepare(
		ctx.Request().Context(),
		relayID,
		request,
	)
	if apiError != nil {
		return api.writeError(ctx, apiError)
	}
	return ctx.JSON(http.StatusOK, response)
}

func (api *API) heartbeat(ctx *echo.Context) error {
	relayID, ok := api.authenticate(ctx)
	if !ok {
		return nil
	}
	var request trafficcontrol.HeartbeatRequest
	if !api.bind(ctx, &request) || request.Validate() != nil ||
		request.RelayID != relayID {
		return api.writeError(
			ctx,
			invalid("traffic heartbeat request is invalid"),
		)
	}
	response, apiError := api.coordinator.Heartbeat(
		ctx.Request().Context(),
		relayID,
		request,
	)
	if apiError != nil {
		return api.writeError(ctx, apiError)
	}
	return ctx.JSON(http.StatusOK, response)
}

func (api *API) finish(ctx *echo.Context) error {
	relayID, ok := api.authenticate(ctx)
	if !ok {
		return nil
	}
	var request trafficcontrol.FinishRequest
	if !api.bind(ctx, &request) || request.Validate() != nil ||
		request.RelayID != relayID {
		return api.writeError(ctx, invalid("traffic finish request is invalid"))
	}
	response, apiError := api.coordinator.Finish(
		ctx.Request().Context(),
		relayID,
		request,
	)
	if apiError != nil {
		return api.writeError(ctx, apiError)
	}
	return ctx.JSON(http.StatusOK, response)
}

func (api *API) authenticate(ctx *echo.Context) (string, bool) {
	identity, err := api.authenticator.Authenticate(ctx.Request())
	if err != nil {
		_ = api.writeError(ctx, &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnauthenticated, Message: "Relay workload identity is invalid", Cause: err,
		})
		return "", false
	}
	relayID, err := identity.RelayID()
	if err != nil {
		_ = api.writeError(ctx, &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnauthenticated, Message: "Relay workload identity is invalid", Cause: err,
		})
		return "", false
	}
	return relayID, true
}

func (api *API) bind(ctx *echo.Context, destination any) bool {
	request := ctx.Request()
	request.Body = http.MaxBytesReader(
		ctx.Response(),
		request.Body,
		trafficcontrol.MaximumBodyBytes,
	)
	return ctx.Bind(destination) == nil
}

func (api *API) writeError(
	ctx *echo.Context,
	apiError *controlplaneapi.Error,
) error {
	status := http.StatusInternalServerError
	switch apiError.Code {
	case controlplaneapi.CodeUnauthenticated:
		status = http.StatusUnauthorized
	case controlplaneapi.CodeForbidden:
		status = http.StatusForbidden
	case controlplaneapi.CodeNotFound:
		status = http.StatusNotFound
	case controlplaneapi.CodeConflict:
		status = http.StatusConflict
	case controlplaneapi.CodeInvalidArgument:
		status = http.StatusBadRequest
	case controlplaneapi.CodeUnavailable:
		status = http.StatusServiceUnavailable
	case controlplaneapi.CodeVersionMismatch:
		status = http.StatusUpgradeRequired
	case controlplaneapi.CodeRateLimited:
		status = http.StatusTooManyRequests
	case controlplaneapi.CodeInternal:
		status = http.StatusInternalServerError
	}
	return ctx.JSON(status, map[string]any{"error": map[string]string{
		"code": string(apiError.Code), "message": apiError.Message,
	}})
}

func invalid(message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Message: message,
	}
}

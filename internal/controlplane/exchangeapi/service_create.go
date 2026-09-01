package exchangeapi

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/labstack/echo/v5"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

func (handler *Service) create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	var spec Spec
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	if apiError := normalizeRequest(&spec); apiError != nil {
		return apiError
	}
	key, apiError := taskapi.IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	sessions, err := handler.bindingSessions()
	if err != nil {
		return internalError(err)
	}
	resolved, err := handler.services.ResolveService(
		request.Context(), identity, session.Namespace, spec.Service, spec.Ports,
	)
	if err != nil {
		return targetError(err)
	}
	if session.Generation > math.MaxInt64 {
		return internalError(errors.New("session generation exceeds the supported range"))
	}
	taskID := trafficbindingclient.TaskIDForIdempotency(
		session.ID, TaskType, identity.Subject, key,
	)
	binding, err := trafficbindingclient.NewPendingInterceptBinding(
		trafficv1alpha1.TrafficBindingModeExchange,
		trafficbindingclient.Owner{
			IdentityID: identity.Subject, SessionID: session.ID,
			TaskID: taskID, SessionGeneration: int64(session.Generation),
		},
		session.Namespace, resolved.Name, resolved.ClusterIP,
		resolved.Ports, spec.LocalTargets,
	)
	if err != nil {
		return internalError(err)
	}
	current, created, err := sessions.EnsureSession(request.Context(), binding)
	if err != nil {
		return storageError(err)
	}
	location := fmt.Sprintf(
		"%s/sessions/%s/exchanges/%s?namespace=%s",
		controlplane.APIPathPrefix, session.ID, taskID, session.Namespace,
	)
	ctx.Response().Header().Set("Location", location)
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(ctx, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created],
		exchangeDocument(current, session))
	return nil
}

package trafficapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionroute"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

// ListDocument is the list envelope every traffic task API returns. The
// element type stays the API's own document, so the wire contracts remain
// separate even though the envelope does not vary.
type ListDocument[D any] struct {
	Items []D `json:"items"`
}

// Handlers serves the REST surface every traffic task API exposes, over that
// API's own spec type S and document type D. It embeds Relay, so an API that
// embeds Handlers gets the traffic-control handshake as well.
type Handlers[S any, D any] struct {
	Relay

	// TaskType is the durable task type recorded against the Session. It also
	// scopes the idempotency key, so two task types may reuse one key.
	TaskType string
	// PathSegment is this API's collection in the Location header:
	// "exchanges", "mirrors" or "previews".
	PathSegment string
	// Normalize validates and canonicalizes a bound create request.
	Normalize func(*S) *controlplaneapi.Error
	// NewBinding turns a normalized request into the pending TrafficBinding to
	// persist. Exchange and Mirror resolve an existing Service here; Preview
	// describes the one it will create.
	NewBinding func(
		context.Context,
		controlplaneapi.Identity,
		sessionapi.ActiveSession,
		trafficbindingclient.Owner,
		S,
	) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error)
	// Document renders one binding as this API's wire document.
	Document func(*trafficv1alpha1.TrafficBinding, sessionapi.ActiveSession) D
	// DeleteBinding removes the durable binding and everything it owns.
	DeleteBinding func(context.Context, string, string) error
}

// Create persists one task, replaying an earlier response when the same
// Idempotency-Key arrives again rather than creating a second task.
func (handlers Handlers[S, D]) Create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	var spec S
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	if apiError := handlers.Normalize(&spec); apiError != nil {
		return apiError
	}
	key, apiError := taskapi.IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	bindings, apiError := handlers.bindings()
	if apiError != nil {
		return apiError
	}
	if session.Generation > math.MaxInt64 {
		return handlers.Task.Errors().Internal(
			errors.New("session generation exceeds the supported range"),
		)
	}
	owner := trafficbindingclient.Owner{
		IdentityID: identity.Subject, SessionID: session.ID,
		TaskID: trafficbindingclient.TaskIDForIdempotency(
			session.ID, handlers.TaskType, identity.Subject, key,
		),
		SessionGeneration: int64(session.Generation),
	}
	binding, apiError := handlers.NewBinding(request.Context(), identity, session, owner, spec)
	if apiError != nil {
		return apiError
	}
	current, created, err := bindings.EnsureSession(request.Context(), binding)
	if err != nil {
		return handlers.Task.Errors().Storage(err)
	}
	ctx.Response().Header().Set("Location", controlplane.APIPathPrefix+
		"/sessions/"+session.ID+"/"+handlers.PathSegment+"/"+owner.TaskID+
		"?namespace="+session.Namespace)
	status := http.StatusCreated
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		status = http.StatusOK
	}
	taskapi.WriteJSON(ctx, status, handlers.Document(current, session))
	return nil
}

func (handlers Handlers[S, D]) Get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	binding, apiError := handlers.OwnedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	taskapi.WriteJSON(ctx, http.StatusOK, handlers.Document(binding, session))
	return nil
}

func (handlers Handlers[S, D]) List(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	bindings, apiError := handlers.bindings()
	if apiError != nil {
		return apiError
	}
	stored, err := bindings.ListSessions(ctx.Request().Context(), session.Namespace, session.ID)
	if err != nil {
		return handlers.Task.Errors().Internal(err)
	}
	items := make([]D, 0, len(stored))
	for index := range stored {
		if handlers.Task.Owns(&stored[index], identity, session) {
			items = append(items, handlers.Document(&stored[index], session))
		}
	}
	taskapi.WriteJSON(ctx, http.StatusOK, ListDocument[D]{Items: items})
	return nil
}

// Pause releases the Kubernetes resources but keeps the durable task, so the
// client can resume it without a new Idempotency-Key.
func (handlers Handlers[S, D]) Pause(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	if _, apiError := handlers.OwnedBinding(
		ctx.Request().Context(), identity, session, taskID,
	); apiError != nil {
		return apiError
	}
	bindings, apiError := handlers.bindings()
	if apiError != nil {
		return apiError
	}
	if err := handlers.Release(ctx.Request().Context(), session.Namespace, taskID); err != nil {
		return handlers.Task.Errors().Internal(err)
	}
	binding, err := bindings.GetSession(ctx.Request().Context(), session.Namespace, taskID)
	if err != nil {
		return handlers.Task.Errors().Internal(err)
	}
	taskapi.WriteJSON(ctx, http.StatusOK, handlers.Document(binding, session))
	return nil
}

func (handlers Handlers[S, D]) Resume(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	binding, apiError := handlers.OwnedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		return handlers.Task.Errors().Storage(errors.New(
			strings.ToLower(handlers.Task.Name) + " Session is not paused",
		))
	}
	bindings, apiError := handlers.bindings()
	if apiError != nil {
		return apiError
	}
	if err := bindings.ResetRelay(ctx.Request().Context(), binding); err != nil {
		return handlers.Task.Errors().Internal(err)
	}
	binding, _ = bindings.GetSession(ctx.Request().Context(), session.Namespace, taskID)
	taskapi.WriteJSON(ctx, http.StatusAccepted, handlers.Document(binding, session))
	return nil
}

// Delete renders the document before removing the binding, so the response
// still describes the task the caller asked to delete.
func (handlers Handlers[S, D]) Delete(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	binding, apiError := handlers.OwnedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	document := handlers.Document(binding, session)
	if err := handlers.DeleteBinding(
		ctx.Request().Context(), session.Namespace, taskID,
	); err != nil {
		return handlers.Task.Errors().Internal(err)
	}
	taskapi.WriteJSON(ctx, http.StatusOK, document)
	return nil
}

// Endpoints wires this API's REST surface onto the Session routes. Stop is an
// alias for Pause: stopping a traffic task releases its Kubernetes resources
// without discarding the durable task.
func (handlers Handlers[S, D]) Endpoints() controlplane.RemoteTaskEndpoints {
	sessions := handlers.Sessions
	return controlplane.RemoteTaskEndpoints{
		Create: sessionroute.WithSession(sessions, handlers.Create),
		Get:    sessionroute.WithTask(sessions, handlers.Get),
		List:   sessionroute.WithSession(sessions, handlers.List),
		Pause:  sessionroute.WithTask(sessions, handlers.Pause),
		Resume: sessionroute.WithTask(sessions, handlers.Resume),
		Delete: sessionroute.WithTask(sessions, handlers.Delete),
		Stop:   sessionroute.WithTask(sessions, handlers.Pause),
	}
}

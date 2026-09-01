package exchangeapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

func (handler *Service) get(
	ctx *echo.Context, identity controlplaneapi.Identity,
	session sessionapi.ActiveSession, taskID string,
) *controlplaneapi.Error {
	binding, apiError := handler.ownedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	writeJSON(ctx, http.StatusOK, exchangeDocument(binding, session))
	return nil
}

func (handler *Service) list(
	ctx *echo.Context, identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	sessions, err := handler.bindingSessions()
	if err != nil {
		return internalError(err)
	}
	bindings, err := sessions.ListSessions(ctx.Request().Context(), session.Namespace, session.ID)
	if err != nil {
		return internalError(err)
	}
	items := make([]Document, 0, len(bindings))
	for index := range bindings {
		if ownedExchange(&bindings[index], identity, session) {
			items = append(items, exchangeDocument(&bindings[index], session))
		}
	}
	writeJSON(ctx, http.StatusOK, listDocument{Items: items})
	return nil
}

func (handler *Service) pause(
	ctx *echo.Context, identity controlplaneapi.Identity,
	session sessionapi.ActiveSession, taskID string,
) *controlplaneapi.Error {
	_, apiError := handler.ownedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	if err := handler.resources.Restore(
		ctx.Request().Context(), serviceSnapshot(session.Namespace), taskID,
	); err != nil {
		return internalError(err)
	}
	sessions, _ := handler.bindingSessions()
	binding, err := sessions.GetSession(ctx.Request().Context(), session.Namespace, taskID)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, exchangeDocument(binding, session))
	return nil
}

func (handler *Service) resume(
	ctx *echo.Context, identity controlplaneapi.Identity,
	session sessionapi.ActiveSession, taskID string,
) *controlplaneapi.Error {
	binding, apiError := handler.ownedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		return storageError(errors.New("exchange Session is not paused"))
	}
	sessions, _ := handler.bindingSessions()
	if err := sessions.ResetRelay(ctx.Request().Context(), binding); err != nil {
		return internalError(err)
	}
	binding, _ = sessions.GetSession(ctx.Request().Context(), session.Namespace, taskID)
	writeJSON(ctx, http.StatusAccepted, exchangeDocument(binding, session))
	return nil
}

func (handler *Service) delete(
	ctx *echo.Context, identity controlplaneapi.Identity,
	session sessionapi.ActiveSession, taskID string,
) *controlplaneapi.Error {
	binding, apiError := handler.ownedBinding(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	document := exchangeDocument(binding, session)
	if err := deleteExchangeBinding(
		ctx.Request().Context(), handler.resources, session.Namespace, taskID,
	); err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}

func (handler *Service) ownedBinding(
	ctx context.Context, identity controlplaneapi.Identity,
	session sessionapi.ActiveSession, taskID string,
) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return nil, notFound()
	}
	sessions, err := handler.bindingSessions()
	if err != nil {
		return nil, internalError(err)
	}
	binding, err := sessions.GetSession(ctx, session.Namespace, taskID)
	if err != nil || !ownedExchange(binding, identity, session) {
		if err != nil && !errors.Is(err, trafficbindingclient.ErrTrafficBindingNotFound) {
			return nil, internalError(err)
		}
		return nil, notFound()
	}
	return binding, nil
}

func serviceSnapshot(namespace string) servicebinding.ServiceInterceptSnapshot {
	return servicebinding.ServiceInterceptSnapshot{Namespace: namespace}
}

func deleteExchangeBinding(
	ctx context.Context, resources ResourceMutator, namespace, taskID string,
) error {
	deleter, ok := resources.(interface {
		DeleteBinding(context.Context, string, string) error
	})
	if !ok {
		return errors.New("exchange deletion is unavailable")
	}
	return deleter.DeleteBinding(ctx, namespace, taskID)
}

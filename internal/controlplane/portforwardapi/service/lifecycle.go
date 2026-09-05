package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

func (service *Service) List(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) ([]PortForward, *controlplaneapi.Error) {
	bindings, err := service.bindings.List(ctx, session.Namespace, session.ID)
	if err != nil {
		return nil, internalError(err)
	}
	items := make([]PortForward, 0, len(bindings))
	for index := range bindings {
		binding := &bindings[index]
		if ownedBinding(binding, identity, session) && isPortForward(binding) {
			items = append(items, portForwardFromBinding(binding, session))
		}
	}
	return items, nil
}

func (service *Service) Pause(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	_, apiError := service.ownedBinding(ctx, identity, session, taskID)
	if apiError != nil {
		return PortForward{}, apiError
	}
	if err := service.bindings.Stop(ctx, session.Namespace, taskID); err != nil {
		return PortForward{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Port Forward cleanup is pending", Cause: err,
		}
	}
	binding, err := service.bindings.Get(ctx, session.Namespace, taskID)
	if err != nil {
		return PortForward{}, internalError(err)
	}
	return portForwardFromBinding(binding, session), nil
}

func (service *Service) Resume(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	binding, apiError := service.ownedBinding(ctx, identity, session, taskID)
	if apiError != nil {
		return PortForward{}, apiError
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		return PortForward{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: "Port Forward Session is not paused",
		}
	}
	resumer, ok := service.bindings.(interface {
		Resume(context.Context, string, string) error
	})
	if !ok {
		return PortForward{}, internalError(errors.New("port Forward resume is unavailable"))
	}
	if err := resumer.Resume(ctx, session.Namespace, taskID); err != nil {
		return PortForward{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnavailable, Message: "Port Forward resume failed", Cause: err,
		}
	}
	binding, _ = service.bindings.Get(ctx, session.Namespace, taskID)
	return portForwardFromBinding(binding, session), nil
}

func (service *Service) Delete(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	binding, apiError := service.ownedBinding(ctx, identity, session, taskID)
	if apiError != nil {
		return PortForward{}, apiError
	}
	result := portForwardFromBinding(binding, session)
	if err := service.bindings.Delete(ctx, session.Namespace, taskID); err != nil {
		return PortForward{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Port Forward deletion is pending", Cause: err,
		}
	}
	return result, nil
}

func (service *Service) ownedBinding(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return nil, controlplaneapi.NotFound()
	}
	binding, err := service.bindings.Get(ctx, session.Namespace, taskID)
	if err != nil || !ownedBinding(binding, identity, session) || !isPortForward(binding) {
		if err != nil && !errors.Is(err, trafficbindingclient.ErrTrafficBindingNotFound) {
			return nil, internalError(err)
		}
		return nil, controlplaneapi.NotFound()
	}
	return binding, nil
}

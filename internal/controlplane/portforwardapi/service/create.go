package service

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type CreateResult struct {
	PortForward PortForward
	Created     bool
	Replayed    bool
}

func (service *Service) Create(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	spec Spec,
	idempotencyKey string,
) (CreateResult, *controlplaneapi.Error) {
	if apiError := normalizeSpec(&spec); apiError != nil {
		return CreateResult{}, apiError
	}
	target, err := service.resolver.Resolve(ctx, identity, session.Namespace, spec)
	if err != nil {
		return CreateResult{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Kubernetes Port Forward target resolution failed", Cause: err,
		}
	}
	if err := validateTarget(target); err != nil {
		return CreateResult{}, internalError(err)
	}
	taskID := trafficbindingclient.TaskIDForIdempotency(
		session.ID, TaskType, identity.Subject, idempotencyKey,
	)
	_, getErr := service.bindings.Get(ctx, session.Namespace, taskID)
	replayed := getErr == nil
	managed, err := service.bindings.Activate(ctx, identity, session, taskID, spec, target)
	if err != nil {
		if errors.Is(err, trafficbindingclient.ErrTrafficBindingConflict) {
			return CreateResult{}, mapStorageError(err)
		}
		if managed {
			_ = service.bindings.Delete(context.WithoutCancel(ctx), session.Namespace, taskID)
		}
		return CreateResult{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Kubernetes Port Forward binding failed", Cause: err,
		}
	}
	binding, err := service.bindings.Get(ctx, session.Namespace, taskID)
	if err != nil {
		return CreateResult{}, internalError(err)
	}
	return CreateResult{
		PortForward: portForwardFromBinding(binding, session),
		Created:     !replayed, Replayed: replayed,
	}, nil
}

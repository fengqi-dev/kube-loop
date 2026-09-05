package exchangeapi

import (
	"context"
	"errors"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

// newBinding resolves the Service this Exchange will intercept and describes the
// pending TrafficBinding for it. The rest of the create request -- binding,
// validation, idempotency and the response -- is shared.
func (handler *Service) newBinding(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	owner trafficbindingclient.Owner,
	spec Spec,
) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error) {
	resolved, err := handler.services.ResolveService(
		ctx, identity, session.Namespace, spec.Service, spec.Ports,
	)
	if err != nil {
		return nil, apiErrors.Target(err)
	}
	binding, err := trafficbindingclient.NewPendingInterceptBinding(
		task.Mode, owner, session.Namespace, resolved.Name, resolved.ClusterIP,
		resolved.Ports, spec.LocalTargets,
	)
	if err != nil {
		return nil, apiErrors.Internal(err)
	}
	return binding, nil
}

// deleteBinding removes the durable Exchange binding. The mutator exposes it
// behind a type assertion so the Service dependency stays the narrow
// capture/apply/restore contract.
func (handler *Service) deleteBinding(ctx context.Context, namespace, taskID string) error {
	deleter, ok := handler.resources.(interface {
		DeleteBinding(context.Context, string, string) error
	})
	if !ok {
		return errors.New("exchange deletion is unavailable")
	}
	return deleter.DeleteBinding(ctx, namespace, taskID)
}

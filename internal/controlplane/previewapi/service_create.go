package previewapi

import (
	"context"
	"errors"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

// newBinding describes the Service this Preview will publish. Unlike Exchange
// and Mirror there is nothing to resolve: the Service does not exist yet.
func (handler *Service) newBinding(
	_ context.Context,
	_ controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	owner trafficbindingclient.Owner,
	spec Spec,
) (*trafficv1alpha1.TrafficBinding, *controlplaneapi.Error) {
	return trafficbindingclient.NewPendingPreviewBinding(
		owner, session.Namespace, spec.Name, spec.Ports, spec.LocalTargets,
	), nil
}

// deleteBinding removes the durable Preview binding. The resource manager
// exposes it behind a type assertion so the Service dependency stays the
// narrow create/delete contract.
func (handler *Service) deleteBinding(ctx context.Context, namespace, taskID string) error {
	deleter, ok := handler.resources.(interface {
		DeleteBinding(context.Context, string, string) error
	})
	if !ok {
		return errors.New("preview deletion is unavailable")
	}
	return deleter.DeleteBinding(ctx, namespace, taskID)
}

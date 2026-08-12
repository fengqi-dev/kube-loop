package portforwardapi

import (
	"context"
	"errors"

	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type TrafficBindingManager struct {
	bindings trafficbindingclient.Lifecycle
}

func NewTrafficBindingManager(bindings trafficbindingclient.Lifecycle) (*TrafficBindingManager, error) {
	if bindings == nil {
		return nil, errors.New("Port Forward TrafficBinding lifecycle is required")
	}
	return &TrafficBindingManager{bindings: bindings}, nil
}

func (manager *TrafficBindingManager) Activate(
	ctx context.Context,
	session sessionapi.ActiveSession,
	taskID string,
	spec portforwardservice.Spec,
) (bool, error) {
	binding := trafficbindingclient.NewPortForwardBinding(
		trafficbindingclient.Owner{
			SessionID: session.ID, TaskID: taskID, SessionGeneration: int64(session.Generation),
		},
		session.Namespace, spec.Kind, spec.Name, spec.Protocol, int32(spec.RemotePort),
	)
	_, managed, err := manager.bindings.Activate(ctx, binding)
	return managed, err
}

func (manager *TrafficBindingManager) Delete(ctx context.Context, namespace, taskID string) error {
	return manager.bindings.Delete(ctx, namespace, taskID)
}

var _ portforwardservice.BindingManager = (*TrafficBindingManager)(nil)

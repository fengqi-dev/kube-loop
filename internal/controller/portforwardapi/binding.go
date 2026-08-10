package portforwardapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/trafficbindingclient"
)

type BindingManager interface {
	Activate(context.Context, sessionapi.ActiveSession, string, Spec) (bool, error)
	Delete(context.Context, string, string) error
}

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
	spec Spec,
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

var _ BindingManager = (*TrafficBindingManager)(nil)

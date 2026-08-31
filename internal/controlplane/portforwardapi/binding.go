package portforwardapi

import (
	"context"
	"errors"
	"math"

	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type TrafficBindingManager struct {
	bindings trafficbindingclient.Lifecycle
}

func NewTrafficBindingManager(
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingManager, error) {
	if bindings == nil {
		return nil, errors.New(
			"port forward TrafficBinding lifecycle is required",
		)
	}
	return &TrafficBindingManager{bindings: bindings}, nil
}

func (manager *TrafficBindingManager) Activate(
	ctx context.Context,
	session sessionapi.ActiveSession,
	taskID string,
	spec portforwardservice.Spec,
) (bool, error) {
	if session.Generation > math.MaxInt64 {
		return false, errors.New("session generation exceeds the supported range")
	}
	binding := trafficbindingclient.NewPortForwardBinding(
		trafficbindingclient.Owner{
			SessionID: session.ID, TaskID: taskID, SessionGeneration: int64(session.Generation),
		},
		session.Namespace,
		spec.Kind,
		spec.Name,
		spec.Protocol,
		int32(spec.RemotePort),
	)
	_, managed, err := manager.bindings.Activate(ctx, binding)
	return managed, err
}

func (manager *TrafficBindingManager) Delete(
	ctx context.Context,
	namespace, taskID string,
) error {
	return manager.bindings.Delete(ctx, namespace, taskID)
}

func (manager *TrafficBindingManager) Pause(
	ctx context.Context,
	namespace, taskID string,
) error {
	return manager.bindings.RequestPause(ctx, namespace, taskID)
}

func (manager *TrafficBindingManager) Stop(
	ctx context.Context,
	namespace, taskID string,
) error {
	return manager.Pause(ctx, namespace, taskID)
}

var _ portforwardservice.BindingManager = (*TrafficBindingManager)(nil)

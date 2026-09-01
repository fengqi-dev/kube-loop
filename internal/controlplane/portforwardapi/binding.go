package portforwardapi

import (
	"context"
	"errors"
	"math"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
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
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
	spec portforwardservice.Spec,
	target portforwardservice.Target,
) (bool, error) {
	if session.Generation > math.MaxInt64 {
		return false, errors.New("session generation exceeds the supported range")
	}
	binding := trafficbindingclient.NewPortForwardBinding(
		trafficbindingclient.Owner{
			IdentityID: identity.Subject, SessionID: session.ID,
			TaskID: taskID, SessionGeneration: int64(session.Generation),
		},
		session.Namespace,
		spec.Kind,
		spec.Name,
		spec.Protocol,
		int32(spec.RemotePort),
	)
	binding.Spec.DialAddress = target.Address()
	if spec.LocalPort != 0 {
		localPort := int32(spec.LocalPort)
		binding.Spec.Ports[0].LocalPort = &localPort
	}
	_, managed, err := manager.bindings.Activate(ctx, binding)
	return managed, err
}

func (manager *TrafficBindingManager) Get(
	ctx context.Context, namespace, taskID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	store, ok := manager.bindings.(interface {
		GetSession(context.Context, string, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding Session lookup is unavailable")
	}
	return store.GetSession(ctx, namespace, taskID)
}

func (manager *TrafficBindingManager) List(
	ctx context.Context, namespace, sessionID string,
) ([]trafficv1alpha1.TrafficBinding, error) {
	store, ok := manager.bindings.(interface {
		ListSessions(context.Context, string, string) ([]trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding Session list is unavailable")
	}
	return store.ListSessions(ctx, namespace, sessionID)
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

func (manager *TrafficBindingManager) Resume(
	ctx context.Context, namespace, taskID string,
) error {
	binding, err := manager.Get(ctx, namespace, taskID)
	if err != nil {
		return err
	}
	binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	_, _, err = manager.bindings.Activate(ctx, binding)
	return err
}

var _ portforwardservice.BindingManager = (*TrafficBindingManager)(nil)

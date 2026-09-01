package exchangeapi

import (
	"context"
	"errors"

	"k8s.io/client-go/kubernetes"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type ResourceMutator interface {
	Capture(
		context.Context,
		controlplaneapi.Identity,
		*servicebinding.ServiceInterceptSnapshot,
	) error
	Apply(
		context.Context,
		controlplaneapi.Identity,
		servicebinding.ServiceInterceptSnapshot,
		string,
	) error
	Restore(
		context.Context,
		servicebinding.ServiceInterceptSnapshot,
		string,
	) error
}

type KubernetesMutationProvider interface {
	ClientFor(authorization.Subject) (kubernetes.Interface, error)
}

type TrafficBindingResourceMutator struct {
	provider KubernetesMutationProvider
	bindings trafficbindingclient.Lifecycle
}

func NewTrafficBindingResourceMutator(
	provider KubernetesMutationProvider,
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingResourceMutator, error) {
	if provider == nil || bindings == nil {
		return nil, errors.New(
			"kubernetes Provider and TrafficBinding lifecycle are required",
		)
	}
	return &TrafficBindingResourceMutator{
		provider: provider, bindings: bindings,
	}, nil
}

func (mutator *TrafficBindingResourceMutator) Capture(
	ctx context.Context,
	identity controlplaneapi.Identity,
	snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	client, err := mutator.userClient(identity)
	if err != nil {
		return err
	}
	return servicebinding.CaptureServiceIntercept(ctx, client, snapshot)
}

func (mutator *TrafficBindingResourceMutator) Apply(
	ctx context.Context,
	_ controlplaneapi.Identity,
	snapshot servicebinding.ServiceInterceptSnapshot,
	interceptID string,
) error {
	store, ok := mutator.bindings.(interface {
		GetSession(context.Context, string, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return errors.New("TrafficBinding Session lookup is unavailable")
	}
	binding, err := store.GetSession(ctx, snapshot.Namespace, interceptID)
	if err != nil {
		return err
	}
	_, _, err = mutator.bindings.Activate(ctx, binding)
	return err
}

func (mutator *TrafficBindingResourceMutator) Restore(
	ctx context.Context,
	snapshot servicebinding.ServiceInterceptSnapshot,
	interceptID string,
) error {
	return mutator.bindings.Pause(ctx, snapshot.Namespace, interceptID)
}

func (mutator *TrafficBindingResourceMutator) DeleteBinding(
	ctx context.Context,
	namespace, interceptID string,
) error {
	return mutator.bindings.Delete(ctx, namespace, interceptID)
}

func (mutator *TrafficBindingResourceMutator) EnsureSession(
	ctx context.Context, binding *trafficv1alpha1.TrafficBinding,
) (*trafficv1alpha1.TrafficBinding, bool, error) {
	store, ok := mutator.bindings.(interface {
		EnsureSession(context.Context, *trafficv1alpha1.TrafficBinding) (*trafficv1alpha1.TrafficBinding, bool, error)
	})
	if !ok {
		return nil, false, errors.New("TrafficBinding Session storage is unavailable")
	}
	return store.EnsureSession(ctx, binding)
}

func (mutator *TrafficBindingResourceMutator) GetSession(
	ctx context.Context, namespace, taskID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	store, ok := mutator.bindings.(interface {
		GetSession(context.Context, string, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding Session lookup is unavailable")
	}
	return store.GetSession(ctx, namespace, taskID)
}

func (mutator *TrafficBindingResourceMutator) ListSessions(
	ctx context.Context, namespace, sessionID string,
) ([]trafficv1alpha1.TrafficBinding, error) {
	store, ok := mutator.bindings.(interface {
		ListSessions(context.Context, string, string) ([]trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding Session list is unavailable")
	}
	return store.ListSessions(ctx, namespace, sessionID)
}

func (mutator *TrafficBindingResourceMutator) FindSession(
	ctx context.Context, taskID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	store, ok := mutator.bindings.(interface {
		FindSession(context.Context, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding Session lookup is unavailable")
	}
	return store.FindSession(ctx, taskID)
}

func (mutator *TrafficBindingResourceMutator) ClaimRelay(
	ctx context.Context, binding *trafficv1alpha1.TrafficBinding, relayID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	store, ok := mutator.bindings.(interface {
		ClaimRelay(context.Context, *trafficv1alpha1.TrafficBinding, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding relay claim is unavailable")
	}
	return store.ClaimRelay(ctx, binding, relayID)
}

func (mutator *TrafficBindingResourceMutator) AttachRelay(
	ctx context.Context, binding *trafficv1alpha1.TrafficBinding,
	relayID, address string, ports map[string]int32,
) error {
	store, ok := mutator.bindings.(interface {
		AttachRelay(context.Context, *trafficv1alpha1.TrafficBinding, string, string, map[string]int32) error
	})
	if !ok {
		return errors.New("TrafficBinding relay update is unavailable")
	}
	return store.AttachRelay(ctx, binding, relayID, address, ports)
}

func (mutator *TrafficBindingResourceMutator) RelayHeartbeat(
	ctx context.Context, binding *trafficv1alpha1.TrafficBinding, relayID string,
) error {
	store, ok := mutator.bindings.(interface {
		RelayHeartbeat(context.Context, *trafficv1alpha1.TrafficBinding, string) error
	})
	if !ok {
		return errors.New("TrafficBinding relay heartbeat is unavailable")
	}
	return store.RelayHeartbeat(ctx, binding, relayID)
}

func (mutator *TrafficBindingResourceMutator) FinishRelay(
	ctx context.Context, binding *trafficv1alpha1.TrafficBinding,
	relayID, reason string,
) error {
	store, ok := mutator.bindings.(interface {
		FinishRelay(context.Context, *trafficv1alpha1.TrafficBinding, string, string) error
	})
	if !ok {
		return errors.New("TrafficBinding relay finish is unavailable")
	}
	return store.FinishRelay(ctx, binding, relayID, reason)
}

func (mutator *TrafficBindingResourceMutator) ResetRelay(
	ctx context.Context, binding *trafficv1alpha1.TrafficBinding,
) error {
	store, ok := mutator.bindings.(interface {
		ResetRelay(context.Context, *trafficv1alpha1.TrafficBinding) error
	})
	if !ok {
		return errors.New("TrafficBinding relay reset is unavailable")
	}
	return store.ResetRelay(ctx, binding)
}

func (mutator *TrafficBindingResourceMutator) userClient(
	identity controlplaneapi.Identity,
) (kubernetes.Interface, error) {
	return mutator.provider.ClientFor(authorization.Subject{
		ID: identity.Subject, Groups: append([]string(nil), identity.Groups...),
	})
}

var _ ResourceMutator = (*TrafficBindingResourceMutator)(nil)

func (mutator *TrafficBindingResourceMutator) BindingManager() *trafficbindingclient.Manager {
	manager, _ := mutator.bindings.(*trafficbindingclient.Manager)
	return manager
}

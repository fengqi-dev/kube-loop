package mirrorapi

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

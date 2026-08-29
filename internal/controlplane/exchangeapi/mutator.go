package exchangeapi

import (
	"context"
	"errors"

	"k8s.io/client-go/kubernetes"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
	provider     KubernetesMutationProvider
	repositories controlplanestorage.Repositories
	bindings     trafficbindingclient.Lifecycle
}

func NewTrafficBindingResourceMutator(
	provider KubernetesMutationProvider,
	repositories controlplanestorage.Repositories,
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingResourceMutator, error) {
	if provider == nil || repositories == nil || bindings == nil {
		return nil, errors.New(
			"kubernetes Provider, storage and TrafficBinding lifecycle are required",
		)
	}
	return &TrafficBindingResourceMutator{
		provider:     provider,
		repositories: repositories,
		bindings:     bindings,
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
	owner, err := trafficbindingclient.OwnerForTask(
		ctx,
		mutator.repositories,
		interceptID,
		TaskType,
		snapshot.Namespace,
	)
	if err != nil {
		return err
	}
	binding, err := trafficbindingclient.NewInterceptBinding(
		trafficv1alpha1.TrafficBindingModeExchange,
		owner,
		snapshot,
	)
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

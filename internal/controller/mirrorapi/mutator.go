package mirrorapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	controllerstorage "github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/trafficbindingclient"
	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/internal/operator/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"k8s.io/client-go/kubernetes"
)

type ResourceMutator interface {
	Capture(context.Context, controller.Principal, *servicebinding.ServiceInterceptSnapshot) error
	Apply(context.Context, controller.Principal, servicebinding.ServiceInterceptSnapshot, string) error
	Restore(context.Context, servicebinding.ServiceInterceptSnapshot, string) error
}

type KubernetesMutationProvider interface {
	ClientFor(authorization.Subject) (kubernetes.Interface, error)
}

type TrafficBindingResourceMutator struct {
	provider     KubernetesMutationProvider
	repositories controllerstorage.Repositories
	bindings     trafficbindingclient.Lifecycle
}

func NewTrafficBindingResourceMutator(
	provider KubernetesMutationProvider,
	repositories controllerstorage.Repositories,
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingResourceMutator, error) {
	if provider == nil || repositories == nil || bindings == nil {
		return nil, errors.New("Kubernetes Provider, storage and TrafficBinding lifecycle are required")
	}
	return &TrafficBindingResourceMutator{provider: provider, repositories: repositories, bindings: bindings}, nil
}

func (mutator *TrafficBindingResourceMutator) Capture(
	ctx context.Context,
	principal controller.Principal,
	snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	client, err := mutator.userClient(principal)
	if err != nil {
		return err
	}
	return servicebinding.CaptureServiceIntercept(ctx, client, snapshot)
}

func (mutator *TrafficBindingResourceMutator) Apply(
	ctx context.Context,
	principal controller.Principal,
	snapshot servicebinding.ServiceInterceptSnapshot,
	interceptID string,
) error {
	owner, err := trafficbindingclient.OwnerForTask(ctx, mutator.repositories, interceptID, TaskType, snapshot.Namespace)
	if err != nil {
		return err
	}
	binding, err := trafficbindingclient.NewInterceptBinding(trafficv1alpha1.TrafficBindingModeMirror, owner, snapshot)
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
	return mutator.bindings.Delete(ctx, snapshot.Namespace, interceptID)
}

func (mutator *TrafficBindingResourceMutator) userClient(principal controller.Principal) (kubernetes.Interface, error) {
	return mutator.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
	})
}

var _ ResourceMutator = (*TrafficBindingResourceMutator)(nil)

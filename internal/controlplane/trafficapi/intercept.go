package trafficapi

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

// InterceptResources is the Kubernetes side of a Service-intercepting traffic
// task. Exchange and Mirror both redirect an existing Service at the Gateway
// and later put it back, so they share one implementation.
type InterceptResources interface {
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

// KubernetesMutationProvider hands out a Kubernetes client that acts as the
// requesting user, so a capture can never read more than the user may.
type KubernetesMutationProvider interface {
	ClientFor(authorization.Subject) (kubernetes.Interface, error)
}

// TrafficBindingInterceptResources drives the intercept through the
// TrafficBinding Operator: the Control Plane records intent on the CR and the
// Operator performs the Service mutation.
type TrafficBindingInterceptResources struct {
	provider KubernetesMutationProvider
	bindings trafficbindingclient.Lifecycle
}

func NewTrafficBindingInterceptResources(
	provider KubernetesMutationProvider,
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingInterceptResources, error) {
	if provider == nil || bindings == nil {
		return nil, errors.New(
			"kubernetes Provider and TrafficBinding lifecycle are required",
		)
	}
	return &TrafficBindingInterceptResources{provider: provider, bindings: bindings}, nil
}

func (resources *TrafficBindingInterceptResources) Capture(
	ctx context.Context,
	identity controlplaneapi.Identity,
	snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	client, err := resources.userClient(identity)
	if err != nil {
		return err
	}
	return servicebinding.CaptureServiceIntercept(ctx, client, snapshot)
}

func (resources *TrafficBindingInterceptResources) Apply(
	ctx context.Context,
	_ controlplaneapi.Identity,
	snapshot servicebinding.ServiceInterceptSnapshot,
	interceptID string,
) error {
	store, ok := resources.bindings.(interface {
		GetSession(context.Context, string, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return errors.New("TrafficBinding Session lookup is unavailable")
	}
	binding, err := store.GetSession(ctx, snapshot.Namespace, interceptID)
	if err != nil {
		return err
	}
	_, _, err = resources.bindings.Activate(ctx, binding)
	return err
}

func (resources *TrafficBindingInterceptResources) Restore(
	ctx context.Context,
	snapshot servicebinding.ServiceInterceptSnapshot,
	interceptID string,
) error {
	return resources.bindings.Pause(ctx, snapshot.Namespace, interceptID)
}

func (resources *TrafficBindingInterceptResources) DeleteBinding(
	ctx context.Context,
	namespace, interceptID string,
) error {
	return resources.bindings.Delete(ctx, namespace, interceptID)
}

// BindingManager exposes the TrafficBinding session store the task APIs read
// and write their durable state through.
func (resources *TrafficBindingInterceptResources) BindingManager() *trafficbindingclient.Manager {
	manager, _ := resources.bindings.(*trafficbindingclient.Manager)
	return manager
}

func (resources *TrafficBindingInterceptResources) userClient(
	identity controlplaneapi.Identity,
) (kubernetes.Interface, error) {
	return resources.provider.ClientFor(authorization.Subject{
		ID: identity.Subject, Groups: append([]string(nil), identity.Groups...),
	})
}

var _ InterceptResources = (*TrafficBindingInterceptResources)(nil)

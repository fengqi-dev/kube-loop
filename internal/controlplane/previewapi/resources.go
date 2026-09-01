package previewapi

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

type ResourceManager interface {
	Create(
		context.Context,
		controlplaneapi.Identity,
		servicebinding.PreviewServiceSnapshot,
		string,
	) (*corev1.Service, error)
	Delete(context.Context, servicebinding.PreviewServiceSnapshot, string) error
}

type TrafficBindingResourceManager struct {
	bindings trafficbindingclient.Lifecycle
}

func NewTrafficBindingResourceManager(
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingResourceManager, error) {
	if bindings == nil {
		return nil, errors.New(
			"preview TrafficBinding lifecycle is required",
		)
	}
	return &TrafficBindingResourceManager{
		bindings: bindings,
	}, nil
}

func (manager *TrafficBindingResourceManager) Create(
	ctx context.Context,
	_ controlplaneapi.Identity,
	snapshot servicebinding.PreviewServiceSnapshot,
	previewID string,
) (*corev1.Service, error) {
	store, ok := manager.bindings.(interface {
		GetSession(context.Context, string, string) (*trafficv1alpha1.TrafficBinding, error)
	})
	if !ok {
		return nil, errors.New("TrafficBinding Session lookup is unavailable")
	}
	binding, err := store.GetSession(ctx, snapshot.Namespace, previewID)
	if err != nil {
		return nil, err
	}
	active, managed, err := manager.bindings.Activate(ctx, binding)
	if err != nil {
		if managed {
			return nil, errors.Join(
				servicebinding.ErrPreviewCleanupPending,
				err,
			)
		}
		return nil, err
	}
	if active.Status.ServiceName != snapshot.Service ||
		active.Status.ServiceClusterIP == "" {
		return nil, errors.Join(
			servicebinding.ErrPreviewCleanupPending,
			fmt.Errorf(
				"ready TrafficBinding returned invalid Preview Service status",
			),
		)
	}
	return &corev1.Service{
		Name:      active.Status.ServiceName,
		Namespace: snapshot.Namespace,
		Spec:      corev1.ServiceSpec{ClusterIP: active.Status.ServiceClusterIP},
	}, nil
}

func (manager *TrafficBindingResourceManager) Delete(
	ctx context.Context,
	snapshot servicebinding.PreviewServiceSnapshot,
	previewID string,
) error {
	return manager.bindings.Pause(ctx, snapshot.Namespace, previewID)
}

func (manager *TrafficBindingResourceManager) DeleteBinding(
	ctx context.Context,
	namespace, previewID string,
) error {
	return manager.bindings.Delete(ctx, namespace, previewID)
}

var _ ResourceManager = (*TrafficBindingResourceManager)(nil)

func (manager *TrafficBindingResourceManager) BindingManager() *trafficbindingclient.Manager {
	bindings, _ := manager.bindings.(*trafficbindingclient.Manager)
	return bindings
}

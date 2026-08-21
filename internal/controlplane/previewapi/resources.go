package previewapi

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
	repositories controlplanestorage.Repositories
	bindings     trafficbindingclient.Lifecycle
}

func NewTrafficBindingResourceManager(
	repositories controlplanestorage.Repositories,
	bindings trafficbindingclient.Lifecycle,
) (*TrafficBindingResourceManager, error) {
	if repositories == nil || bindings == nil {
		return nil, errors.New(
			"preview storage and TrafficBinding lifecycle are required",
		)
	}
	return &TrafficBindingResourceManager{
		repositories: repositories,
		bindings:     bindings,
	}, nil
}

func (manager *TrafficBindingResourceManager) Create(
	ctx context.Context,
	_ controlplaneapi.Identity,
	snapshot servicebinding.PreviewServiceSnapshot,
	previewID string,
) (*corev1.Service, error) {
	owner, err := trafficbindingclient.OwnerForTask(
		ctx,
		manager.repositories,
		previewID,
		TaskType,
		snapshot.Namespace,
	)
	if err != nil {
		return nil, err
	}
	binding := trafficbindingclient.NewPreviewBinding(owner, snapshot)
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
		ObjectMeta: metav1.ObjectMeta{
			Name:      active.Status.ServiceName,
			Namespace: snapshot.Namespace,
		},
		Spec: corev1.ServiceSpec{ClusterIP: active.Status.ServiceClusterIP},
	}, nil
}

func (manager *TrafficBindingResourceManager) Delete(
	ctx context.Context,
	snapshot servicebinding.PreviewServiceSnapshot,
	previewID string,
) error {
	return manager.bindings.Delete(ctx, snapshot.Namespace, previewID)
}

var _ ResourceManager = (*TrafficBindingResourceManager)(nil)

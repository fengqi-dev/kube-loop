package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

// ErrTrafficBindingNotFound is returned when the traffic binding for a Session
// does not exist. Callers treat it as a no-op success (there is nothing to
// pause or wait on).
var ErrTrafficBindingNotFound = errors.New("traffic binding not found")

func (manager *Manager) Pause(
	ctx context.Context,
	namespace, taskID string,
) error {
	key, err := manager.requestPause(ctx, namespace, taskID)
	if errors.Is(err, ErrTrafficBindingNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return manager.waitForPaused(ctx, *key)
}

func (manager *Manager) RequestPause(
	ctx context.Context,
	namespace, taskID string,
) error {
	_, err := manager.requestPause(ctx, namespace, taskID)
	if errors.Is(err, ErrTrafficBindingNotFound) {
		return nil
	}
	return err
}

func (manager *Manager) requestPause(
	ctx context.Context,
	namespace, taskID string,
) (*types.NamespacedName, error) {
	name, err := NameForTask(taskID)
	if err != nil {
		return nil, err
	}
	key := types.NamespacedName{
		Namespace: strings.TrimSpace(namespace),
		Name:      name,
	}
	if key.Namespace == "" {
		return nil, errors.New("traffic binding namespace is required")
	}
	binding := &trafficv1alpha1.TrafficBinding{}
	if err := manager.client.Get(ctx, key, binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrTrafficBindingNotFound
		}
		return nil, fmt.Errorf(
			"read TrafficBinding %s/%s for pausing: %w",
			key.Namespace,
			key.Name,
			err,
		)
	}
	if binding.Spec.TaskID != taskID || binding.Labels[taskIDLabel] != taskID ||
		binding.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
		return nil, fmt.Errorf(
			"traffic binding %s/%s is not owned by Task %s",
			key.Namespace,
			key.Name,
			taskID,
		)
	}
	if !binding.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf(
			"traffic binding %s/%s is being deleted",
			key.Namespace,
			key.Name,
		)
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		before := binding.DeepCopy()
		binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
		if err := manager.client.Patch(ctx, binding, client.MergeFrom(before)); err != nil {
			return nil, fmt.Errorf(
				"pause TrafficBinding %s/%s: %w",
				key.Namespace,
				key.Name,
				err,
			)
		}
	}
	return &key, nil
}

func (manager *Manager) waitForPaused(
	ctx context.Context,
	key types.NamespacedName,
) error {
	return wait.PollUntilContextCancel(
		ctx,
		manager.pollInterval,
		true,
		func(ctx context.Context) (bool, error) {
			current := &trafficv1alpha1.TrafficBinding{}
			if err := manager.client.Get(ctx, key, current); err != nil {
				return false, err
			}
			paused := apiMeta.FindStatusCondition(
				current.Status.Conditions,
				trafficv1alpha1.ConditionPaused,
			)
			return paused != nil && paused.Status == metav1.ConditionTrue &&
				current.Status.ObservedGeneration == current.Generation, nil
		},
	)
}

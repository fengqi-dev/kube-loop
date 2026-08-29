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

func (manager *Manager) Pause(
	ctx context.Context,
	namespace, taskID string,
) error {
	name, err := NameForTask(taskID)
	if err != nil {
		return err
	}
	key := types.NamespacedName{
		Namespace: strings.TrimSpace(namespace),
		Name:      name,
	}
	if key.Namespace == "" {
		return errors.New("traffic binding namespace is required")
	}
	binding := &trafficv1alpha1.TrafficBinding{}
	if err := manager.client.Get(ctx, key, binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf(
			"read TrafficBinding %s/%s for pausing: %w",
			key.Namespace,
			key.Name,
			err,
		)
	}
	if binding.Spec.TaskID != taskID || binding.Labels[taskIDLabel] != taskID ||
		binding.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
		return fmt.Errorf(
			"traffic binding %s/%s is not owned by Task %s",
			key.Namespace,
			key.Name,
			taskID,
		)
	}
	if !binding.DeletionTimestamp.IsZero() {
		return fmt.Errorf(
			"traffic binding %s/%s is being deleted",
			key.Namespace,
			key.Name,
		)
	}
	if binding.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		before := binding.DeepCopy()
		binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
		if err := manager.client.Patch(ctx, binding, client.MergeFrom(before)); err != nil {
			return fmt.Errorf(
				"pause TrafficBinding %s/%s: %w",
				key.Namespace,
				key.Name,
				err,
			)
		}
	}
	return wait.PollUntilContextCancel(
		ctx,
		manager.pollInterval,
		true,
		func(ctx context.Context) (bool, error) {
			current := &trafficv1alpha1.TrafficBinding{}
			if err := manager.client.Get(ctx, key, current); err != nil {
				return false, err
			}
			degraded := apiMeta.FindStatusCondition(
				current.Status.Conditions,
				trafficv1alpha1.ConditionDegraded,
			)
			if degraded != nil && degraded.Status == metav1.ConditionTrue {
				return false, fmt.Errorf(
					"traffic binding %s/%s could not pause (%s): %s",
					key.Namespace,
					key.Name,
					degraded.Reason,
					degraded.Message,
				)
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

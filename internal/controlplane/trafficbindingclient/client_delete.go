package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

// Delete requests deletion and waits for the Operator finalizer to restore or
// remove every owned resource. Repeating Delete after completion is safe.
func (manager *Manager) Delete(
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
			"read TrafficBinding %s/%s for deletion: %w",
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
	if binding.DeletionTimestamp.IsZero() {
		if err := manager.client.Delete(ctx, binding); err != nil &&
			!apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"delete TrafficBinding %s/%s: %w",
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
			err := manager.client.Get(ctx, key, current)
			switch {
			case apierrors.IsNotFound(err):
				return true, nil
			case err != nil:
				return false, err
			default:
				return false, nil
			}
		},
	)
}

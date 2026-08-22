package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (reconciler *Reconciler) removeOrphanedBindings(
	ctx context.Context,
) (int, error) {
	bindings := &trafficv1alpha1.TrafficBindingList{}
	if err := reconciler.manager.client.List(ctx, bindings, client.MatchingLabels{
		managedByLabel: managedByValue, controlPlaneIDLabel: reconciler.manager.controlPlaneID,
	}, client.Limit(reconciler.batchSize)); err != nil {
		return 0, fmt.Errorf("list TrafficBindings: %w", err)
	}
	removed := 0
	var result error
	for index := range bindings.Items {
		binding := &bindings.Items[index]
		orphaned, err := reconciler.orphaned(ctx, binding)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !orphaned {
			continue
		}
		cleanupContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			reconciler.cleanupTimeout,
		)
		err = reconciler.manager.Delete(
			cleanupContext,
			binding.Namespace,
			binding.Spec.TaskID,
		)
		cancel()
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		removed++
	}
	return removed, result
}

func (reconciler *Reconciler) orphaned(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (bool, error) {
	task, err := reconciler.tasks.GetByID(ctx, binding.Spec.TaskID)
	if errors.Is(err, controlplanestorage.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf(
			"read Task %s for TrafficBinding %s/%s: %w",
			binding.Spec.TaskID,
			binding.Namespace,
			binding.Name,
			err,
		)
	}
	expectedType, ok := taskTypeForMode(binding.Spec.Mode)
	return task.SessionID != binding.Spec.SessionID ||
		task.Type != expectedType ||
		!ok ||
		task.State.Terminal(), nil
}

func taskTypeForMode(mode trafficv1alpha1.TrafficBindingMode) (string, bool) {
	switch mode {
	case trafficv1alpha1.TrafficBindingModePortForward:
		return "port-forward", true
	case trafficv1alpha1.TrafficBindingModePreview:
		return "preview", true
	case trafficv1alpha1.TrafficBindingModeExchange:
		return taskTypeExchange, true
	case trafficv1alpha1.TrafficBindingModeMirror:
		return "mirror", true
	default:
		return "", false
	}
}

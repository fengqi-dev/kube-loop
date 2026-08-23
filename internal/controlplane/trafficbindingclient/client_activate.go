package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

// Activate creates the immutable Task-owned binding and waits until the
// Operator has observed it. The boolean is true once this task owns a CR,
// including an idempotent replay of an existing identical object.
func (manager *Manager) Activate(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (*trafficv1alpha1.TrafficBinding, bool, error) {
	if binding == nil {
		return nil, false, errors.New("traffic binding is required")
	}
	if strings.TrimSpace(binding.Namespace) == "" {
		return nil, false, errors.New("traffic binding namespace is required")
	}
	name, err := NameForTask(binding.Spec.TaskID)
	if err != nil {
		return nil, false, err
	}
	desired := binding.DeepCopy()
	desired.Name = name
	desired.Namespace = strings.TrimSpace(desired.Namespace)
	desired.TypeMeta = metav1.TypeMeta{
		APIVersion: trafficv1alpha1.SchemeGroupVersion.String(),
		Kind:       "TrafficBinding",
	}
	if desired.Labels == nil {
		desired.Labels = make(map[string]string, 3)
	}
	desired.Labels[managedByLabel] = managedByValue
	desired.Labels[controlPlaneIDLabel] = manager.controlPlaneID
	desired.Labels[taskIDLabel] = desired.Spec.TaskID
	desired.Labels[sessionIDLabel] = desired.Spec.SessionID

	if err := manager.client.Create(ctx, desired); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, false, fmt.Errorf(
				"create TrafficBinding %s/%s: %w",
				desired.Namespace,
				desired.Name,
				err,
			)
		}
		existing := &trafficv1alpha1.TrafficBinding{}
		if getErr := manager.client.Get(ctx, client.ObjectKeyFromObject(desired), existing); getErr != nil {
			return nil, false, fmt.Errorf(
				"read existing TrafficBinding %s/%s: %w",
				desired.Namespace,
				desired.Name,
				getErr,
			)
		}
		if !reflect.DeepEqual(existing.Spec, desired.Spec) ||
			existing.Labels[taskIDLabel] != desired.Spec.TaskID ||
			existing.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
			return nil, false, fmt.Errorf(
				"traffic binding %s/%s conflicts with another Task",
				desired.Namespace,
				desired.Name,
			)
		}
	}

	key := client.ObjectKeyFromObject(desired)
	var current *trafficv1alpha1.TrafficBinding
	err = wait.PollUntilContextCancel(
		ctx,
		manager.pollInterval,
		true,
		func(ctx context.Context) (bool, error) {
			candidate := &trafficv1alpha1.TrafficBinding{}
			if getErr := manager.client.Get(ctx, key, candidate); getErr != nil {
				if apierrors.IsNotFound(getErr) {
					return false, fmt.Errorf(
						"traffic binding %s/%s disappeared before becoming ready",
						key.Namespace,
						key.Name,
					)
				}
				return false, getErr
			}
			if !candidate.DeletionTimestamp.IsZero() {
				return false, fmt.Errorf(
					"traffic binding %s/%s is being deleted",
					key.Namespace,
					key.Name,
				)
			}
			condition := apiMeta.FindStatusCondition(
				candidate.Status.Conditions, trafficv1alpha1.ConditionDegraded,
			)
			if condition != nil &&
				condition.Status == metav1.ConditionTrue {
				return false, fmt.Errorf(
					"traffic binding %s/%s is degraded (%s): %s",
					key.Namespace,
					key.Name,
					condition.Reason,
					condition.Message,
				)
			}
			ready := apiMeta.FindStatusCondition(
				candidate.Status.Conditions,
				trafficv1alpha1.ConditionReady,
			)
			if ready == nil || ready.Status != metav1.ConditionTrue ||
				candidate.Status.ObservedGeneration != candidate.Generation {
				return false, nil
			}
			current = candidate
			return true, nil
		},
	)
	if err != nil {
		return nil, true, fmt.Errorf(
			"wait for TrafficBinding %s/%s: %w",
			key.Namespace,
			key.Name,
			err,
		)
	}
	return current, true, nil
}

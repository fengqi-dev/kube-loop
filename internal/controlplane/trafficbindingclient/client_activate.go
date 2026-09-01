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

// Activate creates the Session binding or rebinds its transport fields and
// waits until the Operator has observed it. The boolean is true once this task
// owns a CR, including an idempotent replay of an existing object.
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
	desired.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	desired.Name = name
	desired.Namespace = strings.TrimSpace(desired.Namespace)
	// Activate also accepts an object returned by GetSession. Treat that object
	// as desired configuration, not as a create payload with server-owned
	// metadata. The subsequent AlreadyExists path reloads the live object before
	// patching it.
	desired.ResourceVersion = ""
	desired.UID = ""
	desired.Generation = 0
	desired.CreationTimestamp = metav1.Time{}
	desired.DeletionTimestamp = nil
	desired.DeletionGracePeriodSeconds = nil
	desired.ManagedFields = nil
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
		if !sameBindingWorkload(existing.Spec, desired.Spec) ||
			existing.Labels[taskIDLabel] != desired.Spec.TaskID ||
			existing.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
			return nil, false, fmt.Errorf(
				"%w: traffic binding %s/%s belongs to another Session",
				ErrTrafficBindingConflict,
				desired.Namespace,
				desired.Name,
			)
		}
		if !reflect.DeepEqual(existing.Spec, desired.Spec) ||
			existing.Labels[sessionIDLabel] != desired.Spec.SessionID {
			before := existing.DeepCopy()
			existing.Spec = *desired.Spec.DeepCopy()
			existing.Labels[managedByLabel] = managedByValue
			existing.Labels[controlPlaneIDLabel] = manager.controlPlaneID
			existing.Labels[taskIDLabel] = desired.Spec.TaskID
			existing.Labels[sessionIDLabel] = desired.Spec.SessionID
			if patchErr := manager.client.Patch(
				ctx,
				existing,
				client.MergeFrom(before),
			); patchErr != nil {
				return nil, true, fmt.Errorf(
					"activate TrafficBinding %s/%s: %w",
					existing.Namespace,
					existing.Name,
					patchErr,
				)
			}
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

func sameBindingWorkload(left, right trafficv1alpha1.TrafficBindingSpec) bool {
	if left.Mode != right.Mode || left.IdentityID != right.IdentityID ||
		left.TaskID != right.TaskID || left.ClusterIP != right.ClusterIP ||
		left.DialAddress != right.DialAddress ||
		!reflect.DeepEqual(left.Target, right.Target) ||
		!reflect.DeepEqual(left.Preview, right.Preview) ||
		len(left.Ports) != len(right.Ports) {
		return false
	}
	for _, leftPort := range left.Ports {
		matched := false
		for _, rightPort := range right.Ports {
			if leftPort.Name == rightPort.Name &&
				leftPort.TargetPort == rightPort.TargetPort &&
				leftPort.Protocol == rightPort.Protocol &&
				leftPort.LocalHost == rightPort.LocalHost &&
				reflect.DeepEqual(leftPort.LocalPort, rightPort.LocalPort) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

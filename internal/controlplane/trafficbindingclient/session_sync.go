package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

type SessionSynchronizer struct {
	bindings *Manager
}

// SessionBinding is the client-facing Session projection whose source of truth
// is one TrafficBinding object. Database tasks are deliberately not consulted
// when listing these records.
type SessionBinding struct {
	ID           string                                     `json:"id"`
	Name         string                                     `json:"name"`
	Namespace    string                                     `json:"namespace"`
	SessionID    string                                     `json:"sessionId"`
	Mode         trafficv1alpha1.TrafficBindingMode         `json:"mode"`
	DesiredState trafficv1alpha1.TrafficBindingDesiredState `json:"desiredState"`
	Phase        trafficv1alpha1.TrafficBindingPhase        `json:"phase"`
	Target       *trafficv1alpha1.TrafficTarget             `json:"target,omitempty"`
	Preview      *trafficv1alpha1.PreviewExposure           `json:"preview,omitempty"`
	Relay        *trafficv1alpha1.RelayEndpoint             `json:"relay,omitempty"`
	Ports        []trafficv1alpha1.TrafficPort              `json:"ports"`
	ServiceName  string                                     `json:"serviceName,omitempty"`
	ClusterIP    string                                     `json:"serviceClusterIp,omitempty"`
	CreatedAt    time.Time                                  `json:"createdAt"`
}

// List returns exactly the TrafficBindings attached to the current transport
// Session. A stale database Task without a CRD can therefore never appear as a
// Session.
func (synchronizer *SessionSynchronizer) List(
	ctx context.Context,
	namespace, sessionID string,
) ([]SessionBinding, error) {
	bindings := &trafficv1alpha1.TrafficBindingList{}
	if err := synchronizer.bindings.client.List(
		ctx,
		bindings,
		client.InNamespace(namespace),
		client.MatchingLabels{
			managedByLabel:      managedByValue,
			controlPlaneIDLabel: synchronizer.bindings.controlPlaneID,
			sessionIDLabel:      sessionID,
		},
	); err != nil {
		return nil, fmt.Errorf("list TrafficBinding Sessions: %w", err)
	}
	items := make([]SessionBinding, 0, len(bindings.Items))
	for index := range bindings.Items {
		binding := &bindings.Items[index]
		if binding.Spec.SessionID != sessionID {
			continue
		}
		items = append(items, SessionBinding{
			ID:           binding.Spec.TaskID,
			Name:         binding.Name,
			Namespace:    binding.Namespace,
			SessionID:    binding.Spec.SessionID,
			Mode:         binding.Spec.Mode,
			DesiredState: binding.Spec.DesiredState,
			Phase:        binding.Status.Phase,
			Target:       binding.Spec.Target.DeepCopy(),
			Preview:      binding.Spec.Preview.DeepCopy(),
			Relay:        binding.Spec.Relay.DeepCopy(),
			Ports: append(
				[]trafficv1alpha1.TrafficPort(nil),
				binding.Spec.Ports...,
			),
			ServiceName: binding.Status.ServiceName,
			ClusterIP:   binding.Status.ServiceClusterIP,
			CreatedAt:   binding.CreationTimestamp.Time,
		})
	}
	slices.SortFunc(items, func(left, right SessionBinding) int {
		return strings.Compare(left.Name, right.Name)
	})
	return items, nil
}

func NewSessionSynchronizer(bindings *Manager) (*SessionSynchronizer, error) {
	if bindings == nil {
		return nil, errors.New("TrafficBinding manager is required")
	}
	return &SessionSynchronizer{bindings: bindings}, nil
}

// Synchronize adopts each recoverable TrafficBinding in a namespace into the
// current transport Session. TrafficBinding UID/name and Task ID remain stable.
func (synchronizer *SessionSynchronizer) Synchronize(
	ctx context.Context,
	identityID, sessionID, namespace string,
	generation uint64,
	_ time.Time,
) error {
	if generation == 0 || generation > math.MaxInt64 {
		return errors.New("transport Session generation is invalid")
	}
	bindings := &trafficv1alpha1.TrafficBindingList{}
	if err := synchronizer.bindings.client.List(
		ctx,
		bindings,
		client.InNamespace(namespace),
		client.MatchingLabels{
			managedByLabel: managedByValue, controlPlaneIDLabel: synchronizer.bindings.controlPlaneID,
		},
	); err != nil {
		return fmt.Errorf("list TrafficBinding Sessions: %w", err)
	}
	for index := range bindings.Items {
		binding := &bindings.Items[index]
		if binding.Spec.IdentityID != identityID ||
			!binding.DeletionTimestamp.IsZero() {
			continue
		}
		if binding.Spec.SessionID == sessionID &&
			binding.Spec.SessionGeneration == int64(generation) {
			continue
		}
		if err := synchronizer.bindings.Pause(ctx, namespace, binding.Spec.TaskID); err != nil {
			return fmt.Errorf("pause TrafficBinding Session %s before adoption: %w", binding.Spec.TaskID, err)
		}
		current := &trafficv1alpha1.TrafficBinding{}
		key := client.ObjectKeyFromObject(binding)
		if err := synchronizer.bindings.client.Get(ctx, key, current); err != nil {
			return fmt.Errorf("reload TrafficBinding Session %s: %w", binding.Spec.TaskID, err)
		}
		before := current.DeepCopy()
		current.Spec.SessionID = sessionID
		current.Spec.SessionGeneration = int64(generation)
		if current.Labels == nil {
			current.Labels = make(map[string]string, 3)
		}
		current.Labels[sessionIDLabel] = sessionID
		if err := synchronizer.bindings.client.Patch(ctx, current, client.MergeFrom(before)); err != nil {
			return fmt.Errorf("adopt TrafficBinding Session %s: %w", binding.Spec.TaskID, err)
		}
	}
	return nil
}

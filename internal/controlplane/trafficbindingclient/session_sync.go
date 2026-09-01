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

// SessionBinding is the client-facing projection whose source of truth is one
// TrafficBinding object. Database tasks are deliberately not consulted when
// listing these records.
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
	DialAddress  string                                     `json:"dialAddress,omitempty"`
	CreatedAt    time.Time                                  `json:"createdAt"`
}

// List returns exactly the user's TrafficBindings selected by their user ID
// label. A stale database Task without a CRD can therefore never appear as a
// Session.
func (synchronizer *SessionSynchronizer) List(
	ctx context.Context,
	namespace, userID string,
) ([]SessionBinding, error) {
	bindings := &trafficv1alpha1.TrafficBindingList{}
	if err := synchronizer.bindings.client.List(
		ctx,
		bindings,
		client.InNamespace(namespace),
		client.MatchingLabels{
			managedByLabel:      managedByValue,
			controlPlaneIDLabel: synchronizer.bindings.controlPlaneID,
			userIDLabel:         userID,
		},
	); err != nil {
		return nil, fmt.Errorf("list TrafficBinding Sessions: %w", err)
	}
	items := make([]SessionBinding, 0, len(bindings.Items))
	for index := range bindings.Items {
		binding := &bindings.Items[index]
		if binding.Spec.IdentityID != userID {
			continue
		}
		clusterIP := binding.Status.ServiceClusterIP
		if clusterIP == "" {
			clusterIP = binding.Spec.ClusterIP
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
			ClusterIP:   clusterIP,
			DialAddress: binding.Spec.DialAddress,
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

// Delete removes one TrafficBinding only when it belongs to the authenticated
// user. The current transport Session is intentionally not part of ownership.
func (synchronizer *SessionSynchronizer) Delete(
	ctx context.Context,
	namespace, userID, taskID string,
) error {
	binding, err := synchronizer.bindings.GetSession(ctx, namespace, taskID)
	if err != nil {
		return err
	}
	if binding.Spec.IdentityID != userID || binding.Labels[userIDLabel] != userID {
		return ErrTrafficBindingNotFound
	}
	return synchronizer.bindings.Delete(ctx, namespace, taskID)
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
	// Backfill the user ID label on legacy objects before adoption. Keep this
	// separate from transport metadata so the label remains queryable even when
	// a later adoption patch conflicts.
	for index := range bindings.Items {
		binding := &bindings.Items[index]
		if binding.Spec.IdentityID != identityID ||
			!binding.DeletionTimestamp.IsZero() ||
			binding.Labels[userIDLabel] == identityID {
			continue
		}
		before := binding.DeepCopy()
		if binding.Labels == nil {
			binding.Labels = make(map[string]string, 1)
		}
		binding.Labels[userIDLabel] = identityID
		if err := synchronizer.bindings.client.Patch(
			ctx,
			binding,
			client.MergeFrom(before),
		); err != nil {
			return fmt.Errorf(
				"label TrafficBinding Session %s: %w",
				binding.Spec.TaskID,
				err,
			)
		}
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
		current := &trafficv1alpha1.TrafficBinding{}
		key := client.ObjectKeyFromObject(binding)
		if err := synchronizer.bindings.client.Get(ctx, key, current); err != nil {
			return fmt.Errorf("reload TrafficBinding Session %s: %w", binding.Spec.TaskID, err)
		}
		before := current.DeepCopy()
		current.Spec.SessionID = sessionID
		current.Spec.SessionGeneration = int64(generation)
		if current.Labels == nil {
			current.Labels = make(map[string]string, 2)
		}
		current.Labels[sessionIDLabel] = sessionID
		current.Labels[userIDLabel] = identityID
		if err := synchronizer.bindings.client.Patch(ctx, current, client.MergeFrom(before)); err != nil {
			return fmt.Errorf("adopt TrafficBinding Session %s: %w", binding.Spec.TaskID, err)
		}
	}
	return nil
}

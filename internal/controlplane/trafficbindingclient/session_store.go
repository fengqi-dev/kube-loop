package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

// EnsureSession creates a durable business Session without waiting for the
// Operator. This is used by relay-backed modes, whose relay assignment arrives
// asynchronously after creation.
func (manager *Manager) EnsureSession(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (*trafficv1alpha1.TrafficBinding, bool, error) {
	if binding == nil || strings.TrimSpace(binding.Namespace) == "" {
		return nil, false, errors.New("TrafficBinding and namespace are required")
	}
	name, err := NameForTask(binding.Spec.TaskID)
	if err != nil {
		return nil, false, err
	}
	desired := binding.DeepCopy()
	desired.Name = name
	desired.Namespace = strings.TrimSpace(desired.Namespace)
	desired.TypeMeta = metav1.TypeMeta{
		APIVersion: trafficv1alpha1.SchemeGroupVersion.String(), Kind: "TrafficBinding",
	}
	if desired.Spec.DesiredState == "" {
		desired.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	}
	manager.setLabels(desired)
	if err := manager.client.Create(ctx, desired); err == nil {
		return desired, true, nil
	} else if !apierrors.IsAlreadyExists(err) {
		return nil, false, fmt.Errorf("create TrafficBinding Session %s/%s: %w", desired.Namespace, name, err)
	}
	existing, err := manager.GetSession(ctx, desired.Namespace, desired.Spec.TaskID)
	if err != nil {
		return nil, false, err
	}
	if !sameBindingWorkload(existing.Spec, desired.Spec) ||
		existing.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
		return nil, false, fmt.Errorf(
			"%w: TrafficBinding Session %s/%s belongs to another owner",
			ErrTrafficBindingConflict,
			desired.Namespace,
			name,
		)
	}
	if existing.Labels[userIDLabel] != desired.Spec.IdentityID {
		before := existing.DeepCopy()
		if existing.Labels == nil {
			existing.Labels = make(map[string]string, 1)
		}
		existing.Labels[userIDLabel] = desired.Spec.IdentityID
		if err := manager.client.Patch(ctx, existing, client.MergeFrom(before)); err != nil {
			return nil, false, fmt.Errorf(
				"label TrafficBinding Session %s/%s: %w",
				desired.Namespace,
				name,
				err,
			)
		}
	}
	return existing, false, nil
}

func (manager *Manager) setLabels(binding *trafficv1alpha1.TrafficBinding) {
	if binding.Labels == nil {
		binding.Labels = make(map[string]string, 5)
	}
	binding.Labels[managedByLabel] = managedByValue
	binding.Labels[controlPlaneIDLabel] = manager.controlPlaneID
	binding.Labels[taskIDLabel] = binding.Spec.TaskID
	binding.Labels[sessionIDLabel] = binding.Spec.SessionID
	binding.Labels[userIDLabel] = binding.Spec.IdentityID
}

func (manager *Manager) GetSession(
	ctx context.Context,
	namespace, taskID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	name, err := NameForTask(taskID)
	if err != nil {
		return nil, err
	}
	binding := &trafficv1alpha1.TrafficBinding{}
	err = manager.client.Get(ctx, types.NamespacedName{
		Namespace: strings.TrimSpace(namespace), Name: name,
	}, binding)
	if apierrors.IsNotFound(err) {
		return nil, ErrTrafficBindingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read TrafficBinding Session: %w", err)
	}
	if binding.Spec.TaskID != taskID ||
		binding.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
		return nil, ErrTrafficBindingNotFound
	}
	return binding, nil
}

// FindSession locates a Session by its globally unique task ID. Gateway
// heartbeat and finish requests intentionally do not carry a namespace.
func (manager *Manager) FindSession(
	ctx context.Context,
	taskID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	items := &trafficv1alpha1.TrafficBindingList{}
	if err := manager.client.List(ctx, items, client.MatchingLabels{
		managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
		taskIDLabel: taskID,
	}); err != nil {
		return nil, fmt.Errorf("find TrafficBinding Session: %w", err)
	}
	if len(items.Items) != 1 {
		return nil, ErrTrafficBindingNotFound
	}
	return items.Items[0].DeepCopy(), nil
}

func (manager *Manager) ListSessions(
	ctx context.Context,
	namespace, sessionID string,
) ([]trafficv1alpha1.TrafficBinding, error) {
	items := &trafficv1alpha1.TrafficBindingList{}
	if err := manager.client.List(ctx, items, client.InNamespace(namespace), client.MatchingLabels{
		managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
		sessionIDLabel: sessionID,
	}); err != nil {
		return nil, fmt.Errorf("list TrafficBinding Sessions: %w", err)
	}
	return items.Items, nil
}

func (manager *Manager) ClaimRelay(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	relayID string,
) (*trafficv1alpha1.TrafficBinding, error) {
	if binding.Status.RelayOwnerID != "" && binding.Status.RelayOwnerID != relayID {
		return nil, errors.New("TrafficBinding Session is already claimed")
	}
	before := binding.DeepCopy()
	binding.Status.RelayOwnerID = relayID
	now := metav1.NewTime(time.Now().UTC())
	binding.Status.RelayHeartbeatAt = &now
	binding.Status.RelayError = ""
	if err := manager.client.Status().Patch(ctx, binding, client.MergeFrom(before)); err != nil {
		return nil, fmt.Errorf("claim TrafficBinding Session: %w", err)
	}
	return binding, nil
}

func (manager *Manager) AttachRelay(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	relayID, address string,
	ports map[string]int32,
) error {
	if binding.Status.RelayOwnerID != relayID {
		return errors.New("TrafficBinding Session is owned by another relay")
	}
	before := binding.DeepCopy()
	binding.Spec.Relay = &trafficv1alpha1.RelayEndpoint{Address: address}
	for index := range binding.Spec.Ports {
		port := &binding.Spec.Ports[index]
		listenPort, ok := ports[portKey(port.Protocol, port.TargetPort)]
		if !ok {
			return fmt.Errorf("relay port is missing for %s/%d", port.Protocol, port.TargetPort)
		}
		port.RelayPort = &listenPort
	}
	binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	return manager.client.Patch(ctx, binding, client.MergeFrom(before))
}

func (manager *Manager) RelayHeartbeat(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	relayID string,
) error {
	if binding.Status.RelayOwnerID != relayID {
		return errors.New("TrafficBinding Session is owned by another relay")
	}
	before := binding.DeepCopy()
	now := metav1.NewTime(time.Now().UTC())
	binding.Status.RelayHeartbeatAt = &now
	return manager.client.Status().Patch(ctx, binding, client.MergeFrom(before))
}

func (manager *Manager) FinishRelay(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	relayID, reason string,
) error {
	if binding.Status.RelayOwnerID != relayID {
		return errors.New("TrafficBinding Session is owned by another relay")
	}
	beforeStatus := binding.DeepCopy()
	binding.Status.RelayOwnerID = ""
	binding.Status.RelayHeartbeatAt = nil
	binding.Status.RelayError = reason
	if err := manager.client.Status().Patch(ctx, binding, client.MergeFrom(beforeStatus)); err != nil {
		return fmt.Errorf("finish TrafficBinding relay: %w", err)
	}
	beforeSpec := binding.DeepCopy()
	binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
	if err := manager.client.Patch(ctx, binding, client.MergeFrom(beforeSpec)); err != nil {
		return fmt.Errorf("pause finished TrafficBinding Session: %w", err)
	}
	return nil
}

func (manager *Manager) ResetRelay(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	beforeStatus := binding.DeepCopy()
	binding.Status.RelayOwnerID = ""
	binding.Status.RelayHeartbeatAt = nil
	binding.Status.RelayError = ""
	if err := manager.client.Status().Patch(ctx, binding, client.MergeFrom(beforeStatus)); err != nil {
		return fmt.Errorf("reset TrafficBinding relay owner: %w", err)
	}
	beforeSpec := binding.DeepCopy()
	binding.Spec.Relay = nil
	for index := range binding.Spec.Ports {
		binding.Spec.Ports[index].RelayPort = nil
	}
	binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	if err := manager.client.Patch(ctx, binding, client.MergeFrom(beforeSpec)); err != nil {
		return fmt.Errorf("reset TrafficBinding relay assignment: %w", err)
	}
	return nil
}

func portKey(protocol trafficv1alpha1.TransportProtocol, port int32) string {
	return strings.ToUpper(string(protocol)) + fmt.Sprintf("/%d", port)
}

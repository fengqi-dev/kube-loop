// Package trafficbindingclient owns the Control Plane-side lifecycle of
// TrafficBinding custom resources. Kubernetes resource mutation remains in
// the independently deployed Operator.
package trafficbindingclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

const (
	bindingNamePrefix   = "kubeloop-"
	managedByLabel      = "app.kubernetes.io/managed-by"
	managedByValue      = "kubeloop-control-plane"
	controlPlaneIDLabel = "traffic.kubeloop.io/control-plane-id"
	taskIDLabel         = "traffic.kubeloop.io/task-id"
	sessionIDLabel      = "traffic.kubeloop.io/session-id"
	defaultPoll         = 100 * time.Millisecond
)

type Config struct {
	PollInterval   time.Duration
	ControlPlaneID string
}

type Lifecycle interface {
	Activate(context.Context, *trafficv1alpha1.TrafficBinding) (*trafficv1alpha1.TrafficBinding, bool, error)
	Delete(context.Context, string, string) error
}

type Manager struct {
	client         client.Client
	pollInterval   time.Duration
	controlPlaneID string
}

func NewForRESTConfig(config *rest.Config, options Config) (*Manager, error) {
	if config == nil {
		return nil, errors.New("TrafficBinding REST configuration is required")
	}
	scheme := runtime.NewScheme()
	if err := trafficv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register TrafficBinding scheme: %w", err)
	}
	kubernetesClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create TrafficBinding client: %w", err)
	}
	return New(kubernetesClient, options)
}

func New(kubernetesClient client.Client, config Config) (*Manager, error) {
	if kubernetesClient == nil {
		return nil, errors.New("TrafficBinding Kubernetes client is required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPoll
	}
	if config.PollInterval < 10*time.Millisecond || config.PollInterval > 10*time.Second {
		return nil, errors.New("TrafficBinding poll interval must be between 10ms and 10s")
	}
	config.ControlPlaneID = controlPlaneID(config.ControlPlaneID)
	return &Manager{
		client: kubernetesClient, pollInterval: config.PollInterval,
		controlPlaneID: config.ControlPlaneID,
	}, nil
}

// Activate creates the immutable Task-owned binding and waits until the
// Operator has observed it. The boolean is true once this task owns a CR,
// including an idempotent replay of an existing identical object.
func (manager *Manager) Activate(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (*trafficv1alpha1.TrafficBinding, bool, error) {
	if binding == nil {
		return nil, false, errors.New("TrafficBinding is required")
	}
	if strings.TrimSpace(binding.Namespace) == "" {
		return nil, false, errors.New("TrafficBinding namespace is required")
	}
	name, err := NameForTask(binding.Spec.TaskID)
	if err != nil {
		return nil, false, err
	}
	desired := binding.DeepCopy()
	desired.Name = name
	desired.Namespace = strings.TrimSpace(desired.Namespace)
	desired.TypeMeta = metav1.TypeMeta{APIVersion: trafficv1alpha1.SchemeGroupVersion.String(), Kind: "TrafficBinding"}
	if desired.Labels == nil {
		desired.Labels = make(map[string]string, 3)
	}
	desired.Labels[managedByLabel] = managedByValue
	desired.Labels[controlPlaneIDLabel] = manager.controlPlaneID
	desired.Labels[taskIDLabel] = desired.Spec.TaskID
	desired.Labels[sessionIDLabel] = desired.Spec.SessionID

	managed := false
	if err := manager.client.Create(ctx, desired); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, false, fmt.Errorf("create TrafficBinding %s/%s: %w", desired.Namespace, desired.Name, err)
		}
		existing := &trafficv1alpha1.TrafficBinding{}
		if getErr := manager.client.Get(ctx, client.ObjectKeyFromObject(desired), existing); getErr != nil {
			return nil, false, fmt.Errorf("read existing TrafficBinding %s/%s: %w", desired.Namespace, desired.Name, getErr)
		}
		if !reflect.DeepEqual(existing.Spec, desired.Spec) ||
			existing.Labels[taskIDLabel] != desired.Spec.TaskID ||
			existing.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
			return nil, false, fmt.Errorf("TrafficBinding %s/%s conflicts with another Task", desired.Namespace, desired.Name)
		}
		managed = true
	} else {
		managed = true
	}

	key := client.ObjectKeyFromObject(desired)
	var current *trafficv1alpha1.TrafficBinding
	err = wait.PollUntilContextCancel(ctx, manager.pollInterval, true, func(ctx context.Context) (bool, error) {
		candidate := &trafficv1alpha1.TrafficBinding{}
		if getErr := manager.client.Get(ctx, key, candidate); getErr != nil {
			if apierrors.IsNotFound(getErr) {
				return false, fmt.Errorf("TrafficBinding %s/%s disappeared before becoming ready", key.Namespace, key.Name)
			}
			return false, getErr
		}
		if !candidate.DeletionTimestamp.IsZero() {
			return false, fmt.Errorf("TrafficBinding %s/%s is being deleted", key.Namespace, key.Name)
		}
		if condition := apiMeta.FindStatusCondition(candidate.Status.Conditions, trafficv1alpha1.ConditionDegraded); condition != nil && condition.Status == metav1.ConditionTrue {
			return false, fmt.Errorf("TrafficBinding %s/%s is degraded (%s): %s", key.Namespace, key.Name, condition.Reason, condition.Message)
		}
		ready := apiMeta.FindStatusCondition(candidate.Status.Conditions, trafficv1alpha1.ConditionReady)
		if ready == nil || ready.Status != metav1.ConditionTrue || candidate.Status.ObservedGeneration != candidate.Generation {
			return false, nil
		}
		current = candidate
		return true, nil
	})
	if err != nil {
		return nil, managed, fmt.Errorf("wait for TrafficBinding %s/%s: %w", key.Namespace, key.Name, err)
	}
	return current, managed, nil
}

// Delete requests deletion and waits for the Operator finalizer to restore or
// remove every owned resource. Repeating Delete after completion is safe.
func (manager *Manager) Delete(ctx context.Context, namespace, taskID string) error {
	name, err := NameForTask(taskID)
	if err != nil {
		return err
	}
	key := types.NamespacedName{Namespace: strings.TrimSpace(namespace), Name: name}
	if key.Namespace == "" {
		return errors.New("TrafficBinding namespace is required")
	}
	binding := &trafficv1alpha1.TrafficBinding{}
	if err := manager.client.Get(ctx, key, binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("read TrafficBinding %s/%s for deletion: %w", key.Namespace, key.Name, err)
	}
	if binding.Spec.TaskID != taskID || binding.Labels[taskIDLabel] != taskID ||
		binding.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
		return fmt.Errorf("TrafficBinding %s/%s is not owned by Task %s", key.Namespace, key.Name, taskID)
	}
	if binding.DeletionTimestamp.IsZero() {
		if err := manager.client.Delete(ctx, binding); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete TrafficBinding %s/%s: %w", key.Namespace, key.Name, err)
		}
	}
	return wait.PollUntilContextCancel(ctx, manager.pollInterval, true, func(ctx context.Context) (bool, error) {
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
	})
}

func controlPlaneID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "kubeloop"
	}
	if len(validation.IsValidLabelValue(value)) == 0 {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return "sha256-" + hex.EncodeToString(digest[:8])
}

func NameForTask(taskID string) (string, error) {
	taskID = strings.ToLower(strings.TrimSpace(taskID))
	parsed, err := uuid.Parse(taskID)
	if err != nil {
		return "", fmt.Errorf("invalid TrafficBinding Task ID: %w", err)
	}
	if parsed.String() != taskID {
		return "", errors.New("TrafficBinding Task ID must use canonical UUID format")
	}
	return bindingNamePrefix + taskID, nil
}

var _ Lifecycle = (*Manager)(nil)

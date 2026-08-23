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
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
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
	Activate(
		context.Context,
		*trafficv1alpha1.TrafficBinding,
	) (*trafficv1alpha1.TrafficBinding, bool, error)
	Delete(context.Context, string, string) error
}

type Manager struct {
	client         client.Client
	pollInterval   time.Duration
	controlPlaneID string
}

func NewForRESTConfig(config *rest.Config, options Config) (*Manager, error) {
	if config == nil {
		return nil, errors.New("traffic binding REST configuration is required")
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
		return nil, errors.New("traffic binding Kubernetes client is required")
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPoll
	}
	if config.PollInterval < 10*time.Millisecond ||
		config.PollInterval > 10*time.Second {
		return nil, errors.New(
			"traffic binding poll interval must be between 10ms and 10s",
		)
	}
	config.ControlPlaneID = controlPlaneID(config.ControlPlaneID)
	return &Manager{
		client: kubernetesClient, pollInterval: config.PollInterval,
		controlPlaneID: config.ControlPlaneID,
	}, nil
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
		return "", errors.New(
			"traffic binding Task ID must use canonical UUID format",
		)
	}
	return bindingNamePrefix + taskID, nil
}

var _ Lifecycle = (*Manager)(nil)

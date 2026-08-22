package kubeapi

import (
	"errors"
	"strings"
	"time"

	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
)

const (
	defaultListLimit = int64(200)
	maximumListLimit = int64(500)
	maximumContinue  = 2048
)

type ClientProvider interface {
	ClientFor(authorization.Subject) (kubernetesclient.Interface, error)
}

type Service struct {
	provider        ClientProvider
	gatewayVersion  string
	inventory       *inventoryWatchHub
	inventoryResync time.Duration
}

type Option func(*Service)

func WithGatewayVersion(gatewayVersion string) Option {
	return func(handler *Service) { handler.gatewayVersion = strings.TrimSpace(gatewayVersion) }
}

func WithInventoryResync(interval time.Duration) Option {
	return func(handler *Service) { handler.inventoryResync = interval }
}

func New(provider ClientProvider, options ...Option) (*Service, error) {
	if provider == nil {
		return nil, errors.New("kubernetes client Provider is required")
	}
	handler := &Service{
		provider: provider, gatewayVersion: "dev",
		inventoryResync: defaultInventoryResync,
	}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	if handler.inventoryResync <= 0 {
		handler.inventoryResync = defaultInventoryResync
	}
	handler.inventory = newInventoryWatchHub(handler.inventoryResync)
	return handler, nil
}

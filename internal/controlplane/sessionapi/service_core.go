package sessionapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

const (
	DefaultSessionTTL  = 2 * time.Minute
	DefaultMaxLifetime = 8 * time.Hour
	IdempotencyHeader  = "Idempotency-Key"
)

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type NetworkDiscoverer interface {
	Discover(context.Context, controlplaneapi.Identity, string) (networkspec.Spec, error)
}

type CapabilityDiscoverer interface {
	DiscoverCapabilities(
		context.Context,
		controlplaneapi.Identity,
		string,
	) (capability.Snapshot, *controlplaneapi.Error)
}

type TrafficBindingSynchronizer interface {
	Synchronize(context.Context, string, string, string, uint64, time.Time) error
}

type TrafficBindingLister interface {
	List(
		ctx context.Context,
		namespace, identityID string,
	) ([]trafficbindingclient.SessionBinding, error)
}

type TrafficBindingDeleter interface {
	Delete(context.Context, string, string, string) error
}

type Config struct {
	ClusterID             string
	SessionTTL            time.Duration
	MaxLifetime           time.Duration
	Now                   func() time.Time
	Networks              NetworkDiscoverer
	Capabilities          CapabilityDiscoverer
	Registry              *sessionregistry.Registry
	TrafficBindings       TrafficBindingSynchronizer
	TrafficBindingLister  TrafficBindingLister
	TrafficBindingDeleter TrafficBindingDeleter
}

type Service struct {
	storage               Storage
	clusterID             string
	sessionTTL            time.Duration
	maxLifetime           time.Duration
	now                   func() time.Time
	networks              NetworkDiscoverer
	capabilities          CapabilityDiscoverer
	registry              *sessionregistry.Registry
	trafficBindings       TrafficBindingSynchronizer
	trafficBindingLister  TrafficBindingLister
	trafficBindingDeleter TrafficBindingDeleter
}

func New(storageBackend Storage, config Config) (*Service, error) {
	if storageBackend == nil || config.Networks == nil || config.Capabilities == nil {
		return nil, errors.New(
			"session storage, NetworkSpec and capability discoverers are required",
		)
	}
	config.ClusterID = strings.TrimSpace(config.ClusterID)
	if config.ClusterID == "" || len(config.ClusterID) > 256 {
		return nil, errors.New("session cluster ID is required")
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = DefaultSessionTTL
	}
	if config.SessionTTL < 30*time.Second || config.SessionTTL > 30*time.Minute {
		return nil, errors.New("session TTL must be between 30 seconds and 30 minutes")
	}
	if config.MaxLifetime <= 0 {
		config.MaxLifetime = DefaultMaxLifetime
	}
	if config.MaxLifetime < config.SessionTTL || config.MaxLifetime > 24*time.Hour {
		return nil, errors.New("session maximum lifetime must be between the TTL and 24 hours")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Registry == nil {
		config.Registry = sessionregistry.New(context.Background())
	}
	return &Service{
		storage: storageBackend, clusterID: config.ClusterID, sessionTTL: config.SessionTTL,
		maxLifetime: config.MaxLifetime, now: config.Now, networks: config.Networks,
		capabilities: config.Capabilities, registry: config.Registry,
		trafficBindings:       config.TrafficBindings,
		trafficBindingLister:  config.TrafficBindingLister,
		trafficBindingDeleter: config.TrafficBindingDeleter,
	}, nil
}

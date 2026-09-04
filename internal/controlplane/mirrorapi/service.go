package mirrorapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

const TaskType = "mirror"

type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type ServiceResolver interface {
	ResolveService(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		[]servicemodel.Port,
	) (servicemodel.ResolvedService, error)
}

// ResourceMutator is the Kubernetes side of this API. Exchange and Mirror
// intercept a Service the same way, so both use the shared implementation in
// internal/controlplane/trafficapi.
type ResourceMutator = trafficapi.InterceptResources

type Service struct {
	// Relay carries the traffic-control handshake every traffic task API
	// serves identically: Claim, Heartbeat and Finish.
	trafficapi.Relay

	sessions  SessionValidator
	services  ServiceResolver
	resources ResourceMutator
	config    Config
}

func New(
	sessions SessionValidator,
	services ServiceResolver,
	resources ResourceMutator,
	config Config,
) (*Service, error) {
	if sessions == nil || services == nil || resources == nil {
		return nil, errors.New(
			"mirror Session validator, Service resolver and resource mutator are required",
		)
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	service := &Service{
		sessions: sessions, services: services, resources: resources, config: config,
	}
	service.Relay = trafficapi.Relay{
		Task: task, Sessions: sessions, Bindings: service.bindingSessions,
		ServiceName: trafficapi.ServiceNameFromTarget,
		Release:     service.release, ReleaseTimeout: config.RestoreTimeout,
	}
	return service, nil
}

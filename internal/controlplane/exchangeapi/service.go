package exchangeapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

const TaskType = "exchange"

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
	// Handlers carries everything the three traffic task APIs do identically:
	// the REST surface over this API's own Spec and Document, and the
	// traffic-control handshake it embeds in turn.
	trafficapi.Handlers[Spec, Document]

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
			"exchange Session validator, Service resolver and resource mutator are required",
		)
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	service := &Service{
		sessions: sessions, services: services, resources: resources, config: config,
	}
	service.Handlers = trafficapi.Handlers[Spec, Document]{
		Task: task, Sessions: sessions, Bindings: service.bindingSessions,
		ServiceName: trafficapi.ServiceNameFromTarget,
		Release:     service.release, ReleaseTimeout: config.RestoreTimeout,
		TaskType: TaskType, PathSegment: "exchanges",
		Normalize:     normalizeRequest,
		NewBinding:    service.newBinding,
		Document:      exchangeDocument,
		DeleteBinding: service.deleteBinding,
	}
	return service, nil
}

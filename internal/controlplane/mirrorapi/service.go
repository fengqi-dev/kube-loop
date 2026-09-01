package mirrorapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
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
		[]entity.Port,
	) (entity.ResolvedService, error)
}

type Service struct {
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
	return &Service{
		sessions: sessions, services: services, resources: resources, config: config,
	}, nil
}

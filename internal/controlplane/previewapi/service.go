package previewapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
)

const TaskType = "preview"

type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Service struct {
	// Relay carries the traffic-control handshake every traffic task API
	// serves identically: Claim, Heartbeat and Finish.
	trafficapi.Relay

	sessions  SessionValidator
	resources ResourceManager
	config    Config
}

func New(
	sessions SessionValidator,
	resources ResourceManager,
	config Config,
) (*Service, error) {
	if sessions == nil || resources == nil {
		return nil, errors.New(
			"preview Session validator and resource manager are required",
		)
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	service := &Service{
		sessions:  sessions,
		resources: resources,
		config:    config,
	}
	service.Relay = trafficapi.Relay{
		Task: task, Sessions: sessions, Bindings: service.bindingSessions,
		ServiceName: trafficapi.ServiceNameFromPreview,
		Release:     service.release, ReleaseTimeout: config.DeleteTimeout,
	}
	return service, nil
}

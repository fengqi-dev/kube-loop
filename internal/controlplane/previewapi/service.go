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
	// Handlers carries everything the three traffic task APIs do identically:
	// the REST surface over this API's own Spec and Document, and the
	// traffic-control handshake it embeds in turn.
	trafficapi.Handlers[Spec, Document]

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
	service.Handlers = trafficapi.Handlers[Spec, Document]{
		Task: task, Sessions: sessions, Bindings: service.bindingSessions,
		ServiceName: trafficapi.ServiceNameFromPreview,
		Release:     service.release, ReleaseTimeout: config.DeleteTimeout,
		TaskType: TaskType, PathSegment: "previews",
		Normalize:     normalizeRequest,
		NewBinding:    service.newBinding,
		Document:      previewDocument,
		DeleteBinding: service.deleteBinding,
	}
	return service, nil
}

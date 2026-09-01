package previewapi

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
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
	return &Service{
		sessions:  sessions,
		resources: resources,
		config:    config,
	}, nil
}

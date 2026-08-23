package previewapi

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const TaskType = "preview"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Service struct {
	storage   Storage
	sessions  SessionValidator
	resources ResourceManager
	now       func() time.Time
	config    Config
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	resources ResourceManager,
	config Config,
) (*Service, error) {
	if storageBackend == nil || sessions == nil || resources == nil {
		return nil, errors.New(
			"preview storage, Session validator and resource manager are required",
		)
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	return &Service{
		storage:   storageBackend,
		sessions:  sessions,
		resources: resources,
		now:       config.Now,
		config:    config,
	}, nil
}

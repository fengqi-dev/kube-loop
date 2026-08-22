package fileopsapi

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const (
	TaskType      = "pod-file-operation"
	ActionCreate  = "create"
	ActionRename  = "rename"
	ActionDelete  = "delete"
	KindFile      = "file"
	KindDirectory = "directory"
	KindSymlink   = "symlink"
	KindOther     = "other"
)

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

type Config struct {
	Now              func() time.Time
	AllowedPathRoots []string
}

type Service struct {
	storage      Storage
	sessions     SessionValidator
	targets      fileapi.TargetResolver
	operator     Operator
	now          func() time.Time
	allowedRoots []string
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	targets fileapi.TargetResolver,
	operator Operator,
	config Config,
) (*Service, error) {
	if storageBackend == nil || sessions == nil || targets == nil ||
		operator == nil {
		return nil, errors.New(
			"remote file storage, Session validator, target resolver and operator are required",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	roots, err := fileapi.NormalizeAllowedRoots(config.AllowedPathRoots)
	if err != nil {
		return nil, err
	}
	return &Service{
		storage:      storageBackend,
		sessions:     sessions,
		targets:      targets,
		operator:     operator,
		now:          config.Now,
		allowedRoots: roots,
	}, nil
}

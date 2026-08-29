package service

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const TaskType = "port-forward"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type Resolver interface {
	Resolve(
		context.Context,
		controlplaneapi.Identity,
		string,
		Spec,
	) (Target, error)
}

type BindingManager interface {
	Activate(
		context.Context,
		sessionapi.ActiveSession,
		string,
		Spec,
	) (bool, error)
	Stop(context.Context, string, string) error
	Delete(context.Context, string, string) error
}

type Config struct {
	Now func() time.Time
}

type Service struct {
	storage  Storage
	resolver Resolver
	bindings BindingManager
	now      func() time.Time
}

func New(
	storageBackend Storage,
	resolver Resolver,
	bindings BindingManager,
	config Config,
) (*Service, error) {
	if storageBackend == nil || resolver == nil || bindings == nil {
		return nil, errors.New(
			"port forward storage, target resolver and TrafficBinding manager are required",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{
		storage:  storageBackend,
		resolver: resolver,
		bindings: bindings,
		now:      config.Now,
	}, nil
}

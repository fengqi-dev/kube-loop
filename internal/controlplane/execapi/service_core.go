package execapi

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const TaskType = "pod-exec"

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
	Now                     func() time.Time
	CredentialCheckInterval time.Duration
	Authorizer              authorization.Authorizer
}

type Service struct {
	storage                 Storage
	sessions                SessionValidator
	executor                Executor
	now                     func() time.Time
	credentialCheckInterval time.Duration
	authorizer              authorization.Authorizer
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	executor Executor,
	config Config,
) (*Service, error) {
	if storageBackend == nil || sessions == nil || executor == nil {
		return nil, errors.New(
			"pod exec storage, Session validator and Kubernetes executor are required",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.CredentialCheckInterval == 0 {
		config.CredentialCheckInterval = 500 * time.Millisecond
	}
	if config.CredentialCheckInterval < 10*time.Millisecond ||
		config.CredentialCheckInterval > 30*time.Second {
		return nil, errors.New("pod exec credential check interval must be between 10ms and 30s")
	}
	return &Service{
		storage: storageBackend, sessions: sessions, executor: executor, now: config.Now,
		credentialCheckInterval: config.CredentialCheckInterval,
		authorizer:              config.Authorizer,
	}, nil
}

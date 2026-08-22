package fileapi

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

const (
	TaskType          = "file-transfer"
	DirectionUpload   = "upload"
	DirectionDownload = "download"
	KindFile          = "file"
	KindDirectory     = "directory"
	defaultMaxBytes   = uint64(1 << 30)
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

type TargetResolver interface {
	ResolveContainer(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		string,
	) (string, error)
}

type Config struct {
	Now                     func() time.Time
	MaximumBytes            uint64
	AllowedPathRoots        []string
	CredentialCheckInterval time.Duration
	Authorizer              authorization.Authorizer
}

type Service struct {
	storage                 Storage
	sessions                SessionValidator
	targets                 TargetResolver
	executor                TransferExecutor
	now                     func() time.Time
	maximumBytes            uint64
	allowedRoots            []string
	credentialCheckInterval time.Duration
	authorizer              authorization.Authorizer
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	targets TargetResolver,
	executor TransferExecutor,
	config Config,
) (*Service, error) {
	if storageBackend == nil || sessions == nil || targets == nil ||
		executor == nil {
		return nil, errors.New(
			"file transfer storage, Session validator, target resolver and executor are required",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = defaultMaxBytes
	}
	if config.MaximumBytes < filestream.MaximumData ||
		config.MaximumBytes > 1<<40 {
		return nil, errors.New(
			"file transfer maximum size must be between 256 KiB and 1 TiB",
		)
	}
	if config.CredentialCheckInterval == 0 {
		config.CredentialCheckInterval = 500 * time.Millisecond
	}
	if config.CredentialCheckInterval < 10*time.Millisecond ||
		config.CredentialCheckInterval > 30*time.Second {
		return nil, errors.New(
			"file transfer credential check interval must be between 10ms and 30s",
		)
	}
	roots, err := normalizeRoots(config.AllowedPathRoots)
	if err != nil {
		return nil, err
	}
	return &Service{
		storage: storageBackend, sessions: sessions, targets: targets, executor: executor, now: config.Now,
		maximumBytes: config.MaximumBytes, allowedRoots: roots, credentialCheckInterval: config.CredentialCheckInterval,
		authorizer: config.Authorizer,
	}, nil
}

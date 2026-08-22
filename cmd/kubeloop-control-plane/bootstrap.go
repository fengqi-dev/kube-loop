package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type bootstrapRuntime struct {
	Store              *controlplanestorage.Store
	MaintenanceWorker  *maintenance.Worker
	LocalUsers         *adminlocaluser.Service
	IAMBootstrap       *adminbootstrap.Service
	Authorizer         authorization.Authorizer
	KubernetesConfig   controlplanekubernetes.Config
	KubernetesProvider *controlplanekubernetes.Provider
}

func bootstrapControlPlane(
	signalContext context.Context,
	config loadedControlPlaneConfig,
	logger *slog.Logger,
) (_ *bootstrapRuntime, resultErr error) {
	storageConfig := config.Storage
	stateStore, err := controlplanestorage.Open(signalContext, storageConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize control plane storage: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, stateStore.Close())
		}
	}()
	maintenanceWorker, err := maintenance.New(stateStore, logger, maintenance.Config{
		Interval: config.MaintenanceInterval, BatchSize: config.Document.Maintenance.BatchSize,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Control Plane maintenance worker: %w", err)
	}
	localUsers, err := initializeLocalUsers(signalContext, stateStore)
	if err != nil {
		return nil, fmt.Errorf("initialize Management Plane administrator: %w", err)
	}
	iamBootstrap, err := adminbootstrap.New(stateStore, localUsers)
	if err != nil {
		return nil, fmt.Errorf("initialize IAM bootstrap: %w", err)
	}
	bootstrapConfig := config.Document.Admin.Bootstrap
	if bootstrapConfig.Enabled {
		var configuredPassword []byte
		if bootstrapConfig.PasswordFile != "" {
			configuredPassword, err = readInitialPasswordFile(bootstrapConfig.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("load default IAM identity password: %w", err)
			}
		}
		result, initialPassword, created, bootstrapErr := iamBootstrap.CompleteDefault(
			signalContext,
			adminbootstrap.DefaultRequest{
				Username: bootstrapConfig.Username, Password: configuredPassword,
				DisplayName: bootstrapConfig.DisplayName, Email: bootstrapConfig.Email,
				RequestID: uuid.NewString(),
			},
		)
		clear(configuredPassword)
		if bootstrapErr != nil {
			return nil, fmt.Errorf("initialize default IAM identity: %w", bootstrapErr)
		}
		if created {
			if bootstrapConfig.PasswordFile == "" {
				logger.Warn("default IAM identity created; the initial password will not be shown again",
					"username", result.Identity.Username, "initial_password", initialPassword)
			} else {
				logger.Warn("default IAM identity created; retrieve the initial password from its configured Secret",
					"username", result.Identity.Username)
			}
		}
	} else {
		bootstrapToken, bootstrapExpiresAt, bootstrapErr := iamBootstrap.EnsureToken(signalContext)
		if bootstrapErr != nil {
			return nil, fmt.Errorf("initialize IAM bootstrap token: %w", bootstrapErr)
		}
		if bootstrapToken != "" {
			logger.Warn("one-time IAM bootstrap token generated; it will not be shown again",
				"token", bootstrapToken, "expires_at", bootstrapExpiresAt)
		}
	}
	kubernetesConfig := config.Kubernetes
	if kubernetesConfig.UserAgent == controlplanekubernetes.DefaultUserAgent {
		kubernetesConfig.UserAgent = "kubeloop-control-plane/" + version
	}
	kubernetesProvider, err := controlplanekubernetes.NewInCluster(kubernetesConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize in-cluster Kubernetes Provider: %w", err)
	}
	return &bootstrapRuntime{
		Store: stateStore, MaintenanceWorker: maintenanceWorker, LocalUsers: localUsers, IAMBootstrap: iamBootstrap,
		Authorizer:       authorization.NewAuthenticated(),
		KubernetesConfig: kubernetesConfig, KubernetesProvider: kubernetesProvider,
	}, nil
}

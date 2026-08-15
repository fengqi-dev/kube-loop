package main

import (
	"context"
	"log/slog"
	"os"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminiam "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/iam"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

type bootstrapRuntime struct {
	Store                  *controlplanestorage.Store
	MaintenanceWorker      *maintenance.Worker
	LocalUsers             *adminlocaluser.Service
	IAMBootstrap           *adminbootstrap.Service
	ManagementPolicyEngine *adminauthorization.Engine
	IAMLoader              *adminiam.Loader
	PolicyEngine           authorization.Authorizer
	KubernetesConfig       controlplanekubernetes.Config
	KubernetesProvider     *controlplanekubernetes.Provider
}

func bootstrapControlPlane(
	signalContext context.Context,
	config loadedControlPlaneConfig,
	logger *slog.Logger,
) *bootstrapRuntime {
	storageConfig := config.Storage
	stateStore, err := controlplanestorage.Open(signalContext, storageConfig)
	if err != nil {
		logger.Error("initialize control plane storage failed", "error", err)
		os.Exit(1)
	}
	maintenanceWorker, err := maintenance.New(stateStore, logger, maintenance.Config{
		Interval: config.MaintenanceInterval, BatchSize: config.Document.Maintenance.BatchSize,
	})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Control Plane maintenance worker failed", "error", err)
		os.Exit(2)
	}
	localUsers, err := initializeLocalUsers(signalContext, stateStore)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane administrator failed", "error", err)
		os.Exit(2)
	}
	managementPolicyEngine, err := adminauthorization.NewDenyAll()
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane authorization failed", "error", err)
		os.Exit(2)
	}
	iamLoader, err := adminiam.NewLoader(stateStore, managementPolicyEngine)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane policy loader failed", "error", err)
		os.Exit(2)
	}
	iamBootstrap, err := adminbootstrap.New(stateStore, localUsers)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize IAM bootstrap failed", "error", err)
		os.Exit(2)
	}
	bootstrapConfig := config.Document.Admin.Bootstrap
	if bootstrapConfig.Enabled {
		var configuredPassword []byte
		if bootstrapConfig.PasswordFile != "" {
			configuredPassword, err = readInitialPasswordFile(bootstrapConfig.PasswordFile)
			if err != nil {
				_ = stateStore.Close()
				logger.Error("load default IAM identity password failed", "error", err)
				os.Exit(2)
			}
			defer clear(configuredPassword)
		}
		result, initialPassword, created, bootstrapErr := iamBootstrap.CompleteDefault(
			signalContext,
			adminbootstrap.DefaultRequest{
				Username: bootstrapConfig.Username, Password: configuredPassword,
				DisplayName: bootstrapConfig.DisplayName, Email: bootstrapConfig.Email,
				RequestID: uuid.NewString(),
			},
		)
		if bootstrapErr != nil {
			_ = stateStore.Close()
			logger.Error("initialize default IAM identity and organization failed", "error", bootstrapErr)
			os.Exit(2)
		}
		if created {
			if bootstrapConfig.PasswordFile == "" {
				logger.Warn("default IAM identity and organization created; the initial password will not be shown again",
					"username", result.Identity.Username, "initial_password", initialPassword,
					"organization", result.Organization.Name, "organization_slug", result.Organization.Slug)
			} else {
				logger.Warn("default IAM identity and organization created; retrieve the initial password from its configured Secret",
					"username", result.Identity.Username, "organization", result.Organization.Name,
					"organization_slug", result.Organization.Slug)
			}
		}
	} else {
		bootstrapToken, bootstrapExpiresAt, bootstrapErr := iamBootstrap.EnsureToken(signalContext)
		if bootstrapErr != nil {
			_ = stateStore.Close()
			logger.Error("initialize IAM bootstrap token failed", "error", bootstrapErr)
			os.Exit(2)
		}
		if bootstrapToken != "" {
			logger.Warn("one-time IAM bootstrap token generated; it will not be shown again",
				"token", bootstrapToken, "expires_at", bootstrapExpiresAt)
		}
	}
	if err := iamLoader.Load(signalContext); err != nil {
		_ = stateStore.Close()
		logger.Error("load active Management Plane policy failed", "error", err)
		os.Exit(1)
	}
	kubernetesConfig := config.Kubernetes
	if kubernetesConfig.UserAgent == controlplanekubernetes.DefaultUserAgent {
		kubernetesConfig.UserAgent = "kube-loop-control-plane/" + version
	}
	kubernetesProvider, err := controlplanekubernetes.NewInCluster(kubernetesConfig)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize in-cluster Kubernetes Provider failed", "error", err)
		os.Exit(1)
	}
	policyEngine := authorization.NewUnified(managementPolicyEngine, nil)
	return &bootstrapRuntime{
		Store: stateStore, MaintenanceWorker: maintenanceWorker, LocalUsers: localUsers, IAMBootstrap: iamBootstrap,
		ManagementPolicyEngine: managementPolicyEngine, IAMLoader: iamLoader,
		PolicyEngine:     policyEngine,
		KubernetesConfig: kubernetesConfig, KubernetesProvider: kubernetesProvider,
	}
}

package main

import (
	"context"
	"log/slog"
	"os"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/managementconfig"
	adminprovider "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/provider"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type bootstrapRuntime struct {
	Store                     *controlplanestorage.Store
	MaintenanceWorker         *maintenance.Worker
	LocalUsers                *adminlocaluser.Service
	ManagementRevisionService *adminconfig.Service
	ManagementPolicyEngine    *adminauthorization.Engine
	ManagementPolicyLoader    *adminconfig.PolicyLoader
	AuthRegistry              *authn.Registry
	ManagedProviderRuntime    *adminprovider.Runtime
	ManagedProviderService    *adminconfig.ProviderService
	PolicyEngine              authorization.Authorizer
	KubernetesConfig          controlplanekubernetes.Config
	KubernetesProvider        *controlplanekubernetes.Provider
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
	managementFile := config.Management
	localUsers, initialAdmin, err := initializeLocalUsers(signalContext, stateStore, config.Document.Management.PublicURL,
		config.Document.Management.InitialAdmin.UsernameFile,
		config.Document.Management.InitialAdmin.PasswordFile,
		config.Document.Management.InitialAdmin.MFAEncryptionKeyFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane administrator failed", "error", err)
		os.Exit(2)
	}
	managementRevisionService, err := adminconfig.New(stateStore)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane configuration service failed", "error", err)
		os.Exit(2)
	}
	if initialAdmin.PrincipalID != "" {
		bootstrapComplete, err := localUsers.BootstrapComplete(signalContext, initialAdmin.PrincipalID)
		if err != nil {
			_ = stateStore.Close()
			logger.Error("read Management Plane administrator bootstrap state failed", "error", err)
			os.Exit(1)
		}
		if !bootstrapComplete {
			if err := ensureInitialAdminPolicy(signalContext, managementRevisionService, initialAdmin.PrincipalID); err != nil {
				_ = stateStore.Close()
				logger.Error("initialize Management Plane administrator policy failed", "error", err)
				os.Exit(1)
			}
			if err := localUsers.MarkBootstrapComplete(signalContext, initialAdmin.PrincipalID); err != nil {
				_ = stateStore.Close()
				logger.Error("record Management Plane administrator bootstrap failed", "error", err)
				os.Exit(1)
			}
		}
	}
	managementPolicyEngine, err := adminauthorization.NewDenyAll(
		adminauthorization.WithBootstrap(managementFile.Bootstrap.AuthorizationConfig(), stateStore.ManagementState()),
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane authorization failed", "error", err)
		os.Exit(2)
	}
	managementPolicyLoader, err := adminconfig.NewPolicyLoader(stateStore, managementPolicyEngine, 0)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane policy loader failed", "error", err)
		os.Exit(2)
	}
	if err := managementPolicyLoader.Load(signalContext); err != nil {
		_ = stateStore.Close()
		logger.Error("load active Management Plane policy failed", "error", err)
		os.Exit(1)
	}
	authRegistry, err := authn.NewRegistry()
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize authentication registry failed", "error", err)
		os.Exit(1)
	}
	managedProviderRuntime, err := adminprovider.NewRuntime(
		stateStore, authRegistry, config.Document.API.PublicURL, 0,
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize managed authentication Provider runtime failed", "error", err)
		os.Exit(2)
	}
	if err := managedProviderRuntime.Load(signalContext); err != nil {
		_ = stateStore.Close()
		logger.Error("load active managed authentication Providers failed", "error", err)
		os.Exit(1)
	}
	managedProviderService, err := adminconfig.NewProviderService(stateStore, managedProviderRuntime, managedProviderRuntime)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize managed authentication Provider service failed", "error", err)
		os.Exit(2)
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
	policyEngine := authorization.NewUnified(managementPolicyEngine, func(ctx context.Context, namespace string) (map[string]string, error) {
		client, err := kubernetesProvider.SystemClient()
		if err != nil {
			return nil, err
		}
		item, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return item.Labels, nil
	})
	return &bootstrapRuntime{
		Store: stateStore, MaintenanceWorker: maintenanceWorker, LocalUsers: localUsers,
		ManagementRevisionService: managementRevisionService,
		ManagementPolicyEngine:    managementPolicyEngine, ManagementPolicyLoader: managementPolicyLoader,
		AuthRegistry: authRegistry, ManagedProviderRuntime: managedProviderRuntime,
		ManagedProviderService: managedProviderService, PolicyEngine: policyEngine,
		KubernetesConfig: kubernetesConfig, KubernetesProvider: kubernetesProvider,
	}
}

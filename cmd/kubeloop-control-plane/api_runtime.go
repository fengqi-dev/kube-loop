package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/exchangeapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileopsapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/kubeapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/mirrorapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/networkapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/previewapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionregistry"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi"
	ticketservice "github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficcontrolapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

type apiRuntime struct {
	Routes          controlplane.APIRoutes
	RelayRegistry   *relayRegistryRuntime
	SessionRuntime  *sessionregistry.Registry
	SessionRecovery *sessionregistry.Reconciler
	BindingRecovery *trafficbindingclient.Reconciler
}

func buildAPIRuntime(
	ctx context.Context,
	config loadedControlPlaneConfig,
	environment controlPlaneEnvironment,
	logger *slog.Logger,
	store *controlplanestorage.Store,
	policyEngine authorization.Authorizer,
	kubernetesProvider *controlplanekubernetes.Provider,
) *apiRuntime {
	bindingRESTConfig, err := kubernetesProvider.SystemRESTConfig()
	if err != nil {
		_ = store.Close()
		logger.Error("initialize TrafficBinding REST configuration failed", "error", err)
		os.Exit(1)
	}
	trafficBindings, err := trafficbindingclient.NewForRESTConfig(bindingRESTConfig, trafficbindingclient.Config{
		ControlPlaneID: config.Document.API.ServiceID,
	})
	if err != nil {
		_ = store.Close()
		logger.Error("initialize TrafficBinding client failed", "error", err)
		os.Exit(1)
	}
	bindingRecovery, err := trafficbindingclient.NewReconciler(
		trafficBindings, store.Tasks(), store.Sessions(), logger, trafficbindingclient.ReconcilerConfig{},
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize TrafficBinding recovery worker failed", "error", err)
		os.Exit(2)
	}
	kubernetesAPI, err := kubeapi.New(
		kubernetesProvider,
		kubeapi.WithCapabilityAuthorizer(policyEngine),
		kubeapi.WithAuthorizedNamespaces(func(ctx context.Context, identityID string, groupIDs []string) ([]string, error) {
			return store.Groups().ListAuthorizedNamespaces(ctx, identityID, groupIDs)
		}),
		kubeapi.WithGatewayVersion(version),
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Kubernetes API handler failed", "error", err)
		os.Exit(1)
	}
	networkDiscoverer, err := networkapi.NewDiscoverer(kubernetesProvider)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize NetworkSpec discoverer failed", "error", err)
		os.Exit(1)
	}
	sessionRuntime := sessionregistry.New(ctx)
	sessionAPI, err := sessionapi.New(store, sessionapi.Config{
		ClusterID: config.Document.API.ServiceID, SessionTTL: config.SessionTTL, MaxLifetime: config.SessionMaxLifetime,
		Networks: networkDiscoverer, Capabilities: kubernetesAPI, Registry: sessionRuntime,
	})
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Cluster Session API failed", "error", err)
		os.Exit(2)
	}
	sessionRecovery, err := sessionregistry.NewReconciler(
		store, logger, sessionregistry.RecoveryConfig{},
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Session runtime recovery worker failed", "error", err)
		os.Exit(2)
	}
	relaySigningKey, err := relayticket.LoadSigningKey(config.Document.Relay.Ticket.SigningKeyFile)
	if err != nil {
		_ = store.Close()
		logger.Error("load RelayTicket signing key failed", "error", err)
		os.Exit(2)
	}
	relaySigner, err := relayticket.NewSigner(config.Document.Relay.Ticket.KeyID, relaySigningKey)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize RelayTicket signer failed", "error", err)
		os.Exit(2)
	}
	systemClient, err := kubernetesProvider.SystemClient()
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Relay Registry Kubernetes client failed", "error", err)
		os.Exit(1)
	}
	relayRegistry, err := newRelayRegistryRuntime(relayRegistryOptions{
		ListenAddress:   config.Document.Relay.Registry.Listen,
		CertificateFile: config.Document.Relay.Registry.CertificateFile, PrivateKeyFile: config.Document.Relay.Registry.PrivateKeyFile,
		ClientCAFile: config.Document.Relay.Registry.ClientCAFile, AuthenticationMode: config.Document.Relay.Registry.Authentication,
		TokenAudience: config.Document.Relay.Registry.TokenAudience, TrustDomain: config.Document.Relay.Registry.TrustDomain,
		Namespace: config.Document.Relay.Registry.Namespace, ServiceAccount: config.Document.Relay.Registry.ServiceAccount,
		AllowedHosts: config.Document.Relay.Registry.EndpointAllowedHosts, PublicURL: config.Document.API.PublicURL,
		LeaseDuration: config.RelayLeaseDuration, HeartbeatAfter: config.RelayHeartbeatAfter,
		KeyGeneration: config.Document.Relay.Registry.KeyGeneration, KeyValidity: config.RelayKeyValidity,
		TicketKeyID: config.Document.Relay.Ticket.KeyID, TicketSigningKey: relaySigningKey,
		KubernetesClient: systemClient, Context: ctx, ControlPlanePodName: environment.PodName,
		Logger: logger,
	})
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Relay Registry failed", "error", err)
		os.Exit(2)
	}
	if relayRegistry != nil {
		desiredStates, loadErr := store.RelayDesiredStates().List(ctx)
		if loadErr != nil {
			_ = store.Close()
			logger.Error("load durable Relay desired states failed", "error", loadErr)
			os.Exit(2)
		}
		for _, desired := range desiredStates {
			if restoreErr := relayRegistry.registry.RestoreDesiredState(desired.RelayID, relaycontrol.State(desired.DesiredState)); restoreErr != nil {
				_ = store.Close()
				logger.Error("restore durable Relay desired state failed", "relay_id", desired.RelayID, "error", restoreErr)
				os.Exit(2)
			}
		}
	}
	relayTicketService, err := ticketservice.New(ticketservice.Config{
		Issuer: config.Document.API.PublicURL, TTL: config.RelayTicketTTL, Signer: relaySigner,
		Allocator: relayRegistry.registry, Topology: relayRegistry.allocationTopology,
	})
	if err != nil {
		_ = store.Close()
		logger.Error("initialize RelayTicket API failed", "error", err)
		os.Exit(2)
	}
	portForwardResolver, err := controlplanekubernetes.NewPortForwardResolver(kubernetesProvider)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Port Forward target resolver failed", "error", err)
		os.Exit(2)
	}
	portForwardBindings, err := portforwardapi.NewTrafficBindingManager(trafficBindings)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Port Forward TrafficBinding manager failed", "error", err)
		os.Exit(2)
	}
	portForwardService, err := portforwardservice.New(
		store, portForwardResolver, portForwardBindings, portforwardservice.Config{},
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Port Forward Task API failed", "error", err)
		os.Exit(2)
	}
	serviceResolver, err := controlplanekubernetes.NewServiceResolver(kubernetesProvider)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Exchange Service resolver failed", "error", err)
		os.Exit(2)
	}
	exchangeMutator, err := exchangeapi.NewTrafficBindingResourceMutator(kubernetesProvider, store, trafficBindings)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Exchange resource mutator failed", "error", err)
		os.Exit(2)
	}
	exchangeAPI, err := exchangeapi.New(
		store, sessionAPI, serviceResolver, exchangeMutator,
		exchangeapi.Config{},
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Exchange Task API failed", "error", err)
		os.Exit(2)
	}
	mirrorMutator, err := mirrorapi.NewTrafficBindingResourceMutator(kubernetesProvider, store, trafficBindings)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Mirror resource mutator failed", "error", err)
		os.Exit(2)
	}
	mirrorAPI, err := mirrorapi.New(
		store, sessionAPI, serviceResolver, mirrorMutator,
		mirrorapi.Config{},
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Mirror Task API failed", "error", err)
		os.Exit(2)
	}
	previewResources, err := previewapi.NewTrafficBindingResourceManager(store, trafficBindings)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Preview resource manager failed", "error", err)
		os.Exit(2)
	}
	previewAPI, err := previewapi.New(
		store, sessionAPI, previewResources,
		previewapi.Config{},
	)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Preview Task API failed", "error", err)
		os.Exit(2)
	}
	if relayRegistry != nil {
		trafficDispatcher, dispatcherErr := trafficcontrolapi.NewDispatcher(exchangeAPI, mirrorAPI, previewAPI)
		if dispatcherErr != nil {
			_ = store.Close()
			logger.Error("initialize traffic control dispatcher failed", "error", dispatcherErr)
			os.Exit(2)
		}
		trafficAPI, trafficErr := trafficcontrolapi.New(relayRegistry.authenticator, trafficDispatcher)
		if trafficErr == nil {
			trafficErr = relayRegistry.handler.Mount(trafficAPI)
		}
		if trafficErr != nil {
			_ = store.Close()
			logger.Error("initialize Gateway traffic control API failed", "error", trafficErr)
			os.Exit(2)
		}
	}
	podExecutor, err := execapi.NewKubernetesExecutor(kubernetesProvider)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Kubernetes Pod executor failed", "error", err)
		os.Exit(2)
	}
	execAPI, err := execapi.New(store, sessionAPI, podExecutor, execapi.Config{Authorizer: policyEngine})
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Pod exec Task API failed", "error", err)
		os.Exit(2)
	}
	fileTargetResolver, err := controlplanekubernetes.NewContainerResolver(kubernetesProvider)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Kubernetes file target resolver failed", "error", err)
		os.Exit(2)
	}
	fileConfig := config.Files
	fileConfig.Authorizer = policyEngine
	fileExecutor, err := fileapi.NewKubernetesTransferExecutor(podExecutor, fileConfig.MaximumBytes)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Kubernetes file transfer executor failed", "error", err)
		os.Exit(2)
	}
	fileAPI, err := fileapi.New(store, sessionAPI, fileTargetResolver, fileExecutor, fileConfig)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize file transfer Task API failed", "error", err)
		os.Exit(2)
	}
	fileOperator, err := fileopsapi.NewKubernetesOperator(podExecutor)
	if err != nil {
		_ = store.Close()
		logger.Error("initialize Kubernetes remote file operator failed", "error", err)
		os.Exit(2)
	}
	fileOperationsAPI, err := fileopsapi.New(store, sessionAPI, fileTargetResolver, fileOperator, fileopsapi.Config{
		AllowedPathRoots: fileConfig.AllowedPathRoots,
	})
	if err != nil {
		_ = store.Close()
		logger.Error("initialize remote file operation API failed", "error", err)
		os.Exit(2)
	}
	apiRoutes := controlplane.APIRoutes{
		Tickets:        ticketapi.NewRoutes(relayTicketService, sessionAPI).Endpoints(),
		PortForwards:   portforwardapi.NewRoutes(portForwardService, sessionAPI).Endpoints(),
		Exchanges:      exchangeapi.NewRoutes(exchangeAPI).Endpoints(),
		Mirrors:        mirrorapi.NewRoutes(mirrorAPI).Endpoints(),
		Previews:       previewapi.NewRoutes(previewAPI).Endpoints(),
		FileOperations: fileopsapi.NewRoutes(fileOperationsAPI).Endpoints(),
		FileTransfers:  fileapi.NewRoutes(fileAPI).Endpoints(),
		Exec:           execapi.NewRoutes(execAPI).Endpoints(),
		Sessions:       sessionapi.NewRoutes(sessionAPI).Endpoints(),
		Kubernetes:     kubeapi.NewRoutes(kubernetesAPI).Endpoints(),
	}
	return &apiRuntime{
		Routes: apiRoutes, RelayRegistry: relayRegistry,
		SessionRuntime: sessionRuntime, SessionRecovery: sessionRecovery,
		BindingRecovery: bindingRecovery,
	}
}

package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	options "github.com/fengqi-dev/kube-loop/internal/controlplane/config"
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
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficcontrolapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

type apiRuntime struct {
	Routes          controlplane.APIRoutes
	RelayRegistry   *relayRegistryRuntime
	SessionRuntime  *sessionregistry.Registry
	SessionRecovery *sessionregistry.Reconciler
}

type trafficTaskRuntime struct {
	portForwards *portforwardservice.Service
	exchanges    *exchangeapi.Service
	mirrors      *mirrorapi.Service
	previews     *previewapi.Service
}

func buildAPIRuntime(
	ctx context.Context,
	config options.Config,
	environment options.Environment,
	logger *slog.Logger,
	store *controlplanestorage.Store,
	authorizer authorization.Authorizer,
	kubernetesProvider *controlplanekubernetes.Provider,
	info buildinfo.Info,
) (*apiRuntime, error) {
	bindingRESTConfig, err := kubernetesProvider.SystemRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("initialize TrafficBinding REST configuration: %w", err)
	}
	trafficBindings, err := trafficbindingclient.NewForRESTConfig(bindingRESTConfig, trafficbindingclient.Config{
		ControlPlaneID: config.Document.API.ServiceID,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize TrafficBinding client: %w", err)
	}
	kubernetesAPI, err := kubeapi.New(
		kubernetesProvider,
		kubeapi.WithGatewayVersion(info.Version),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Kubernetes API handler: %w", err)
	}
	networkDiscoverer, err := networkapi.NewDiscoverer(kubernetesProvider)
	if err != nil {
		return nil, fmt.Errorf("initialize NetworkSpec discoverer: %w", err)
	}
	sessionRuntime := sessionregistry.New(ctx)
	bindingSessions, err := trafficbindingclient.NewSessionSynchronizer(trafficBindings)
	if err != nil {
		return nil, fmt.Errorf("initialize TrafficBinding Session synchronizer: %w", err)
	}
	sessionAPI, err := sessionapi.New(store, sessionapi.Config{
		ClusterID: config.Document.API.ServiceID, SessionTTL: config.SessionTTL, MaxLifetime: config.SessionMaxLifetime,
		Networks: networkDiscoverer, Capabilities: kubernetesAPI, Registry: sessionRuntime,
		TrafficBindings:       bindingSessions,
		TrafficBindingLister:  bindingSessions,
		TrafficBindingDeleter: bindingSessions,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Cluster Session API: %w", err)
	}
	sessionRecovery, err := sessionregistry.NewReconciler(
		store, logger, sessionregistry.RecoveryConfig{},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Session runtime recovery worker: %w", err)
	}
	relaySigningKey, err := relayticket.LoadSigningKey(config.Document.Relay.Ticket.SigningKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load RelayTicket signing key: %w", err)
	}
	relaySigner, err := relayticket.NewSigner(config.Document.Relay.Ticket.KeyID, relaySigningKey)
	if err != nil {
		return nil, fmt.Errorf("initialize RelayTicket signer: %w", err)
	}
	systemClient, err := kubernetesProvider.SystemClient()
	if err != nil {
		return nil, fmt.Errorf("initialize Relay Registry Kubernetes client: %w", err)
	}
	relayRegistry, err := newRelayRegistryRuntime(relayRegistryOptions{
		ListenAddress:       config.Document.Relay.Registry.Listen,
		CertificateFile:     config.Document.Relay.Registry.CertificateFile,
		PrivateKeyFile:      config.Document.Relay.Registry.PrivateKeyFile,
		ClientCAFile:        config.Document.Relay.Registry.ClientCAFile,
		AuthenticationMode:  config.Document.Relay.Registry.Authentication,
		TokenAudience:       config.Document.Relay.Registry.TokenAudience,
		TrustDomain:         config.Document.Relay.Registry.TrustDomain,
		Namespace:           config.Document.Relay.Registry.Namespace,
		ServiceAccount:      config.Document.Relay.Registry.ServiceAccount,
		AllowedHosts:        config.Document.Relay.Registry.EndpointAllowedHosts,
		PublicURL:           config.Document.API.PublicURL,
		LeaseDuration:       config.RelayLeaseDuration,
		HeartbeatAfter:      config.RelayHeartbeatAfter,
		KeyGeneration:       config.Document.Relay.Registry.KeyGeneration,
		KeyValidity:         config.RelayKeyValidity,
		TicketKeyID:         config.Document.Relay.Ticket.KeyID,
		TicketSigningKey:    relaySigningKey,
		KubernetesClient:    systemClient,
		Context:             ctx,
		ControlPlanePodName: environment.PodName,
		Logger:              logger,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Relay Registry: %w", err)
	}
	relayTicketService, err := ticketservice.New(ticketservice.Config{
		Issuer: config.Document.API.PublicURL, TTL: config.RelayTicketTTL, Signer: relaySigner,
		Allocator: relayRegistry.registry, Topology: relayRegistry.allocationTopology, Logger: logger,
		TrafficEncryption: config.Document.Relay.Ticket.TrafficEncryption,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize RelayTicket API: %w", err)
	}
	trafficTasks, err := buildTrafficTaskRuntime(
		sessionAPI, kubernetesProvider, trafficBindings, relayRegistry,
	)
	if err != nil {
		return nil, err
	}
	podExecutor, err := execapi.NewKubernetesExecutor(kubernetesProvider)
	if err != nil {
		return nil, fmt.Errorf("initialize Kubernetes Pod executor: %w", err)
	}
	execAPI, err := execapi.New(store, sessionAPI, podExecutor, execapi.Config{Authorizer: authorizer})
	if err != nil {
		return nil, fmt.Errorf("initialize Pod exec Task API: %w", err)
	}
	fileTargetResolver, err := controlplanekubernetes.NewContainerResolver(kubernetesProvider)
	if err != nil {
		return nil, fmt.Errorf("initialize Kubernetes file target resolver: %w", err)
	}
	fileConfig := config.Files
	fileConfig.Authorizer = authorizer
	fileExecutor, err := fileapi.NewKubernetesTransferExecutor(podExecutor, fileConfig.MaximumBytes)
	if err != nil {
		return nil, fmt.Errorf("initialize Kubernetes file transfer executor: %w", err)
	}
	fileAPI, err := fileapi.New(store, sessionAPI, fileTargetResolver, fileExecutor, fileConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize file transfer Task API: %w", err)
	}
	fileOperator, err := fileopsapi.NewKubernetesOperator(podExecutor)
	if err != nil {
		return nil, fmt.Errorf("initialize Kubernetes remote file operator: %w", err)
	}
	fileOperationsAPI, err := fileopsapi.New(store, sessionAPI, fileTargetResolver, fileOperator, fileopsapi.Config{
		AllowedPathRoots: fileConfig.AllowedPathRoots,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize remote file operation API: %w", err)
	}
	apiRoutes := controlplane.APIRoutes{
		Tickets:        ticketapi.NewRoutes(relayTicketService, sessionAPI).Endpoints(),
		PortForwards:   portforwardapi.NewRoutes(trafficTasks.portForwards, sessionAPI).Endpoints(),
		Exchanges:      exchangeapi.NewRoutes(trafficTasks.exchanges).Endpoints(),
		Mirrors:        mirrorapi.NewRoutes(trafficTasks.mirrors).Endpoints(),
		Previews:       previewapi.NewRoutes(trafficTasks.previews).Endpoints(),
		FileOperations: fileopsapi.NewRoutes(fileOperationsAPI).Endpoints(),
		FileTransfers:  fileapi.NewRoutes(fileAPI).Endpoints(),
		Exec:           execapi.NewRoutes(execAPI).Endpoints(),
		Sessions:       sessionapi.NewRoutes(sessionAPI).Endpoints(),
		Kubernetes:     kubeapi.NewRoutes(kubernetesAPI).Endpoints(),
	}
	return &apiRuntime{
		Routes: apiRoutes, RelayRegistry: relayRegistry,
		SessionRuntime: sessionRuntime, SessionRecovery: sessionRecovery,
	}, nil
}

func buildTrafficTaskRuntime(
	sessions *sessionapi.Service,
	kubernetesProvider *controlplanekubernetes.Provider,
	trafficBindings *trafficbindingclient.Manager,
	relayRegistry *relayRegistryRuntime,
) (*trafficTaskRuntime, error) {
	portForwardResolver, err := controlplanekubernetes.NewPortForwardResolver(kubernetesProvider)
	if err != nil {
		return nil, fmt.Errorf("initialize Port Forward target resolver: %w", err)
	}
	portForwardBindings, err := portforwardapi.NewTrafficBindingManager(trafficBindings)
	if err != nil {
		return nil, fmt.Errorf("initialize Port Forward TrafficBinding manager: %w", err)
	}
	portForwardService, err := portforwardservice.New(
		portForwardResolver, portForwardBindings, portforwardservice.Config{},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Port Forward Task API: %w", err)
	}
	serviceResolver, err := controlplanekubernetes.NewServiceResolver(kubernetesProvider)
	if err != nil {
		return nil, fmt.Errorf("initialize Exchange Service resolver: %w", err)
	}
	// Exchange and Mirror intercept a Service identically, so one stateless
	// mutator serves both.
	interceptResources, err := trafficapi.NewTrafficBindingInterceptResources(
		kubernetesProvider, trafficBindings,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize Service intercept resources: %w", err)
	}
	exchangeAPI, err := exchangeapi.New(sessions, serviceResolver, interceptResources, exchangeapi.Config{})
	if err != nil {
		return nil, fmt.Errorf("initialize Exchange Task API: %w", err)
	}
	mirrorAPI, err := mirrorapi.New(sessions, serviceResolver, interceptResources, mirrorapi.Config{})
	if err != nil {
		return nil, fmt.Errorf("initialize Mirror Task API: %w", err)
	}
	previewResources, err := previewapi.NewTrafficBindingResourceManager(trafficBindings)
	if err != nil {
		return nil, fmt.Errorf("initialize Preview resource manager: %w", err)
	}
	previewAPI, err := previewapi.New(sessions, previewResources, previewapi.Config{})
	if err != nil {
		return nil, fmt.Errorf("initialize Preview Task API: %w", err)
	}
	if relayRegistry != nil {
		dispatcher, err := trafficcontrolapi.NewDispatcher(exchangeAPI, mirrorAPI, previewAPI)
		if err != nil {
			return nil, fmt.Errorf("initialize traffic control dispatcher: %w", err)
		}
		trafficAPI, err := trafficcontrolapi.New(relayRegistry.authenticator, dispatcher)
		if err == nil {
			err = relayRegistry.handler.Mount(trafficAPI)
		}
		if err != nil {
			return nil, fmt.Errorf("initialize Gateway traffic control API: %w", err)
		}
	}
	return &trafficTaskRuntime{
		portForwards: portForwardService, exchanges: exchangeAPI, mirrors: mirrorAPI, previews: previewAPI,
	}, nil
}

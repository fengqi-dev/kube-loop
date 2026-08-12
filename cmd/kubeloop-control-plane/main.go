package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminbreakglass "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/breakglass"
	managementconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/config"
	adminhttpapi "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpapi"
	adminhttpserver "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpserver"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminprovider "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/provider"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	authconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/config"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/exchangeapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileopsapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/kubeapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/maintenance"
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
	"github.com/fengqi-dev/kube-loop/internal/logging"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/google/uuid"
)

var (
	version     = "dev"
	commit      = "unknown"
	protocolMin = "2.0"
	protocolMax = "2.0"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "storage" {
		commandContext, stopCommand := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		exitCode := runStorageCommand(commandContext, os.Args[2:], os.Stdout, os.Stderr)
		stopCommand()
		os.Exit(exitCode)
	}

	accessTokenTTLDefault, accessTokenTTLError := durationEnvOrDefault("KUBELOOP_ACCESS_TOKEN_TTL", 5*time.Minute)
	refreshTokenTTLDefault, refreshTokenTTLError := durationEnvOrDefault("KUBELOOP_REFRESH_TOKEN_TTL", 30*24*time.Hour)
	sessionTTLDefault, sessionTTLError := durationEnvOrDefault("KUBELOOP_SESSION_TTL", sessionapi.DefaultSessionTTL)
	sessionMaxLifetimeDefault, sessionMaxLifetimeError := durationEnvOrDefault("KUBELOOP_SESSION_MAX_LIFETIME", sessionapi.DefaultMaxLifetime)
	relayTicketTTLDefault, relayTicketTTLError := durationEnvOrDefault("KUBELOOP_RELAY_TICKET_TTL", relayticket.DefaultLifetime)
	relayLeaseDurationDefault, relayLeaseDurationError := durationEnvOrDefault("KUBELOOP_RELAY_LEASE_DURATION", 45*time.Second)
	relayHeartbeatAfterDefault, relayHeartbeatAfterError := durationEnvOrDefault("KUBELOOP_RELAY_HEARTBEAT_AFTER", 10*time.Second)
	relayKeyValidityDefault, relayKeyValidityError := durationEnvOrDefault("KUBELOOP_RELAY_KEY_VALIDITY", 365*24*time.Hour)
	relayKeyGenerationDefault, relayKeyGenerationError := uint64EnvOrDefault("KUBELOOP_RELAY_KEY_GENERATION", 1)
	maintenanceIntervalDefault, maintenanceIntervalError := durationEnvOrDefault("KUBELOOP_MAINTENANCE_INTERVAL", maintenance.DefaultInterval)
	listenAddress := flag.String("listen", envOrDefault("KUBELOOP_LISTEN", controlplane.DefaultListenAddress), "Control Plane listen address")
	publicURL := flag.String("public-url", os.Getenv("KUBELOOP_PUBLIC_URL"), "public Gateway URL (or KUBELOOP_PUBLIC_URL)")
	serviceID := flag.String("service-id", envOrDefault("KUBELOOP_SERVICE_ID", controlplane.DefaultServiceID), "stable service ID")
	tunnelPath := flag.String("tunnel-path", envOrDefault("KUBELOOP_TUNNEL_PATH", controlplane.DefaultTunnelPath), "same-origin WebSocket Data Plane path")
	minClientVersion := flag.String("min-client-version", os.Getenv("KUBELOOP_MIN_CLIENT_VERSION"), "minimum supported client version")
	authConfigFile := flag.String("auth-config", os.Getenv("KUBELOOP_AUTH_CONFIG_FILE"), "authentication provider JSON config file")
	policyConfigFile := flag.String("policy-config", os.Getenv("KUBELOOP_POLICY_CONFIG_FILE"), "Gateway authorization policy JSON file")
	managementConfigFile := flag.String("management-config", os.Getenv("KUBELOOP_MANAGEMENT_CONFIG_FILE"), "Control Plane Management Plane JSON config file")
	managementListenAddress := flag.String("management-listen", envOrDefault("KUBELOOP_MANAGEMENT_LISTEN", adminhttpserver.DefaultListenAddress), "Management Plane listen address")
	managementPublicURL := flag.String("management-public-url", envOrDefault("KUBELOOP_MANAGEMENT_PUBLIC_URL", "http://127.0.0.1:8081"), "Management Plane browser origin")
	initialAdminUsernameFile := flag.String("initial-admin-username-file", os.Getenv("KUBELOOP_INITIAL_ADMIN_USERNAME_FILE"), "initial Management Plane administrator username file")
	initialAdminPasswordFile := flag.String("initial-admin-password-file", os.Getenv("KUBELOOP_INITIAL_ADMIN_PASSWORD_FILE"), "initial Management Plane administrator password file")
	managementMFAKeyFile := flag.String("management-mfa-key-file", os.Getenv("KUBELOOP_MANAGEMENT_MFA_KEY_FILE"), "32-byte Management Plane MFA encryption key file")
	kubernetesConfigFile := flag.String("kubernetes-config", os.Getenv("KUBELOOP_KUBERNETES_CONFIG_FILE"), "Gateway Kubernetes Provider JSON config file")
	tokenSigningKeyFile := flag.String("token-signing-key-file", os.Getenv("KUBELOOP_TOKEN_SIGNING_KEY_FILE"), "Ed25519 PKCS#8 token signing key file")
	tokenKeyID := flag.String("token-key-id", envOrDefault("KUBELOOP_TOKEN_KEY_ID", "primary"), "token signing key ID")
	relayTicketSigningKeyFile := flag.String("relay-ticket-signing-key-file", os.Getenv("KUBELOOP_RELAY_TICKET_SIGNING_KEY_FILE"), "Ed25519 PKCS#8 RelayTicket signing key file")
	relayTicketKeyID := flag.String("relay-ticket-key-id", envOrDefault("KUBELOOP_RELAY_TICKET_KEY_ID", "primary"), "RelayTicket signing key ID")
	relayRegistryListen := flag.String("relay-registry-listen", os.Getenv("KUBELOOP_RELAY_REGISTRY_LISTEN"), "internal Relay Registry listen address")
	relayRegistryCertificateFile := flag.String("relay-registry-cert-file", os.Getenv("KUBELOOP_RELAY_REGISTRY_CERT_FILE"), "Relay Registry server certificate file")
	relayRegistryPrivateKeyFile := flag.String("relay-registry-key-file", os.Getenv("KUBELOOP_RELAY_REGISTRY_KEY_FILE"), "Relay Registry server private key file")
	relayRegistryClientCAFile := flag.String("relay-registry-client-ca-file", os.Getenv("KUBELOOP_RELAY_REGISTRY_CLIENT_CA_FILE"), "Relay Registry client CA file")
	relayRegistryAuthentication := flag.String("relay-registry-authentication", envOrDefault("KUBELOOP_RELAY_REGISTRY_AUTHENTICATION", "mtls"), "Relay Registry workload authentication: mtls or tokenreview")
	relayTokenAudience := flag.String("relay-token-audience", envOrDefault("KUBELOOP_RELAY_TOKEN_AUDIENCE", "kubeloop-relay"), "projected ServiceAccount token audience for tokenreview mode")
	relayTrustDomain := flag.String("relay-trust-domain", envOrDefault("KUBELOOP_RELAY_TRUST_DOMAIN", "cluster.local"), "allowed Relay workload SPIFFE trust domain")
	relayNamespace := flag.String("relay-namespace", os.Getenv("KUBELOOP_RELAY_NAMESPACE"), "allowed Relay workload namespace")
	relayServiceAccount := flag.String("relay-service-account", os.Getenv("KUBELOOP_RELAY_SERVICE_ACCOUNT"), "allowed Relay workload ServiceAccount")
	relayEndpointAllowedHosts := flag.String("relay-endpoint-allowed-hosts", os.Getenv("KUBELOOP_RELAY_ENDPOINT_ALLOWED_HOSTS"), "comma-separated exact hosts or .suffixes for Relay endpoints")
	accessTokenTTL := flag.Duration("access-token-ttl", accessTokenTTLDefault, "access token lifetime")
	refreshTokenTTL := flag.Duration("refresh-token-ttl", refreshTokenTTLDefault, "refresh token family lifetime")
	sessionTTL := flag.Duration("session-ttl", sessionTTLDefault, "Cluster Session heartbeat lifetime")
	sessionMaxLifetime := flag.Duration("session-max-lifetime", sessionMaxLifetimeDefault, "maximum Cluster Session lifetime")
	relayTicketTTL := flag.Duration("relay-ticket-ttl", relayTicketTTLDefault, "RelayTicket lifetime")
	relayLeaseDuration := flag.Duration("relay-lease-duration", relayLeaseDurationDefault, "Relay Registry lease lifetime")
	relayHeartbeatAfter := flag.Duration("relay-heartbeat-after", relayHeartbeatAfterDefault, "Relay heartbeat interval")
	relayKeyValidity := flag.Duration("relay-key-validity", relayKeyValidityDefault, "published RelayTicket verification-key validity")
	relayKeyGeneration := flag.Uint64("relay-key-generation", relayKeyGenerationDefault, "published RelayTicket verification-key generation")
	maintenanceInterval := flag.Duration("maintenance-interval", maintenanceIntervalDefault, "expired storage record cleanup interval")
	maintenanceBatchSize := flag.Int("maintenance-batch-size", maintenance.DefaultBatchSize, "maximum records removed per storage type and pass")
	shutdownTimeout := flag.Duration("shutdown-timeout", controlplane.DefaultShutdownTimeout, "graceful shutdown timeout")
	apiRequestTimeout := flag.Duration("api-request-timeout", controlplane.DefaultAPIRequestTimeout, "API request timeout")
	maxRequestBodyBytes := flag.Int64("max-request-body-bytes", controlplane.DefaultMaxRequestBodyBytes, "maximum API request body size")
	logLevel := flag.String("log-level", envOrDefault("KUBELOOP_LOG_LEVEL", "info"), "log level: debug, info, warn, or error")
	flag.Parse()

	parsedLogLevel, err := logging.ParseLevel(*logLevel)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid log level", "error", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsedLogLevel}))
	if accessTokenTTLError != nil || refreshTokenTTLError != nil || sessionTTLError != nil || sessionMaxLifetimeError != nil ||
		relayTicketTTLError != nil || relayLeaseDurationError != nil || relayHeartbeatAfterError != nil ||
		relayKeyValidityError != nil || relayKeyGenerationError != nil || maintenanceIntervalError != nil {
		logger.Error("invalid token duration environment configuration")
		os.Exit(2)
	}
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	storageConfig, err := controlplanestorage.ConfigFromEnv()
	if err != nil {
		logger.Error("invalid storage configuration", "error", err)
		os.Exit(2)
	}
	stateStore, err := controlplanestorage.Open(signalContext, storageConfig)
	if err != nil {
		logger.Error("initialize control plane storage failed", "error", err)
		os.Exit(1)
	}
	maintenanceWorker, err := maintenance.New(stateStore, logger, maintenance.Config{
		Interval: *maintenanceInterval, BatchSize: *maintenanceBatchSize,
	})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Control Plane maintenance worker failed", "error", err)
		os.Exit(2)
	}
	authFile, err := authconfig.Load(*authConfigFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("load authentication configuration failed", "error", err)
		os.Exit(2)
	}
	managementFile, err := managementconfig.Load(*managementConfigFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("load Management Plane configuration failed", "error", err)
		os.Exit(2)
	}
	breakGlassStore, err := adminbreakglass.New(managementFile.BreakGlass)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane emergency authentication failed", "error", err)
		os.Exit(2)
	}
	if managementFile.BreakGlass.Enabled {
		if _, err := breakGlassStore.CurrentBreakGlassState(signalContext); err != nil {
			_ = stateStore.Close()
			logger.Error("validate Management Plane emergency credential failed", "error", err)
			os.Exit(2)
		}
	}
	localUsers, initialAdmin, err := initializeLocalUsers(signalContext, stateStore, *managementPublicURL,
		*initialAdminUsernameFile, *initialAdminPasswordFile, *managementMFAKeyFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane administrator failed", "error", err)
		os.Exit(2)
	}
	managementRevisionService, err := adminrevision.New(stateStore)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane revision service failed", "error", err)
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
		adminauthorization.WithBreakGlass(breakGlassStore),
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane authorization failed", "error", err)
		os.Exit(2)
	}
	managementPolicyLoader, err := adminrevision.NewPolicyLoader(stateStore, managementPolicyEngine, 0)
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
	authRegistry, err := authconfig.Build(signalContext, authFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize authentication providers failed", "error", err)
		os.Exit(1)
	}
	managedProviderRuntime, err := adminprovider.NewRuntime(
		stateStore, authRegistry, authFile, managementFile.ProviderSecretAliases, *publicURL, 0,
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
	managedProviderService, err := adminrevision.NewProviderService(stateStore, managedProviderRuntime, managedProviderRuntime)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize managed authentication Provider service failed", "error", err)
		os.Exit(2)
	}
	policy, err := authorization.Load(*policyConfigFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("load Gateway policy failed", "error", err)
		os.Exit(2)
	}
	policyEngine, err := authorization.New(policy)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Gateway policy failed", "error", err)
		os.Exit(2)
	}
	kubernetesConfig, err := controlplanekubernetes.Load(*kubernetesConfigFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("load Kubernetes Provider configuration failed", "error", err)
		os.Exit(2)
	}
	if kubernetesConfig.UserAgent == controlplanekubernetes.DefaultUserAgent {
		kubernetesConfig.UserAgent = "kube-loop-control-plane/" + version
	}
	kubernetesProvider, err := controlplanekubernetes.NewInCluster(kubernetesConfig)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize in-cluster Kubernetes Provider failed", "error", err)
		os.Exit(1)
	}
	bindingRESTConfig, err := kubernetesProvider.SystemRESTConfig()
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize TrafficBinding REST configuration failed", "error", err)
		os.Exit(1)
	}
	trafficBindings, err := trafficbindingclient.NewForRESTConfig(bindingRESTConfig, trafficbindingclient.Config{})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize TrafficBinding client failed", "error", err)
		os.Exit(1)
	}
	bindingRecovery, err := trafficbindingclient.NewReconciler(
		trafficBindings, stateStore.Tasks(), stateStore.Sessions(), logger, trafficbindingclient.ReconcilerConfig{},
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize TrafficBinding recovery worker failed", "error", err)
		os.Exit(2)
	}
	kubernetesAPI, err := kubeapi.New(
		kubernetesProvider,
		kubeapi.WithCapabilityAuthorizer(policyEngine),
		kubeapi.WithGatewayVersion(version),
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Kubernetes API handler failed", "error", err)
		os.Exit(1)
	}
	networkDiscoverer, err := networkapi.NewDiscoverer(kubernetesProvider)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize NetworkSpec discoverer failed", "error", err)
		os.Exit(1)
	}
	sessionRuntime := sessionregistry.New(signalContext)
	sessionAPI, err := sessionapi.New(stateStore, sessionapi.Config{
		ClusterID: *serviceID, SessionTTL: *sessionTTL, MaxLifetime: *sessionMaxLifetime,
		Networks: networkDiscoverer, Capabilities: kubernetesAPI, Registry: sessionRuntime,
	})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Cluster Session API failed", "error", err)
		os.Exit(2)
	}
	sessionRecovery, err := sessionregistry.NewReconciler(
		stateStore, logger, sessionregistry.RecoveryConfig{},
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Session runtime recovery worker failed", "error", err)
		os.Exit(2)
	}
	relaySigningKey, err := relayticket.LoadSigningKey(*relayTicketSigningKeyFile)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("load RelayTicket signing key failed", "error", err)
		os.Exit(2)
	}
	relaySigner, err := relayticket.NewSigner(*relayTicketKeyID, relaySigningKey)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize RelayTicket signer failed", "error", err)
		os.Exit(2)
	}
	systemClient, err := kubernetesProvider.SystemClient()
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Relay Registry Kubernetes client failed", "error", err)
		os.Exit(1)
	}
	relayRegistry, err := newRelayRegistryRuntime(relayRegistryOptions{
		ListenAddress:   *relayRegistryListen,
		CertificateFile: *relayRegistryCertificateFile, PrivateKeyFile: *relayRegistryPrivateKeyFile,
		ClientCAFile: *relayRegistryClientCAFile, AuthenticationMode: *relayRegistryAuthentication,
		TokenAudience: *relayTokenAudience, TrustDomain: *relayTrustDomain,
		Namespace: *relayNamespace, ServiceAccount: *relayServiceAccount,
		AllowedHosts: *relayEndpointAllowedHosts, PublicURL: *publicURL,
		LeaseDuration: *relayLeaseDuration, HeartbeatAfter: *relayHeartbeatAfter,
		KeyGeneration: *relayKeyGeneration, KeyValidity: *relayKeyValidity,
		TicketKeyID: *relayTicketKeyID, TicketSigningKey: relaySigningKey,
		KubernetesClient: systemClient, Context: signalContext, ControlPlanePodName: os.Getenv("KUBELOOP_POD_NAME"),
		Logger: logger,
	})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Relay Registry failed", "error", err)
		os.Exit(2)
	}
	if relayRegistry != nil {
		desiredStates, loadErr := stateStore.RelayDesiredStates().List(signalContext)
		if loadErr != nil {
			_ = stateStore.Close()
			logger.Error("load durable Relay desired states failed", "error", loadErr)
			os.Exit(2)
		}
		for _, desired := range desiredStates {
			if restoreErr := relayRegistry.registry.RestoreDesiredState(desired.RelayID, relaycontrol.State(desired.DesiredState)); restoreErr != nil {
				_ = stateStore.Close()
				logger.Error("restore durable Relay desired state failed", "relay_id", desired.RelayID, "error", restoreErr)
				os.Exit(2)
			}
		}
	}
	relayTicketService, err := ticketservice.New(ticketservice.Config{
		Issuer: *publicURL, TTL: *relayTicketTTL, Signer: relaySigner,
		Allocator: relayRegistry.registry, Topology: relayRegistry.allocationTopology,
	})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize RelayTicket API failed", "error", err)
		os.Exit(2)
	}
	portForwardResolver, err := controlplanekubernetes.NewPortForwardResolver(kubernetesProvider)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Port Forward target resolver failed", "error", err)
		os.Exit(2)
	}
	portForwardBindings, err := portforwardapi.NewTrafficBindingManager(trafficBindings)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Port Forward TrafficBinding manager failed", "error", err)
		os.Exit(2)
	}
	portForwardService, err := portforwardservice.New(
		stateStore, portForwardResolver, portForwardBindings, portforwardservice.Config{},
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Port Forward Task API failed", "error", err)
		os.Exit(2)
	}
	serviceResolver, err := controlplanekubernetes.NewServiceResolver(kubernetesProvider)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Exchange Service resolver failed", "error", err)
		os.Exit(2)
	}
	exchangeMutator, err := exchangeapi.NewTrafficBindingResourceMutator(kubernetesProvider, stateStore, trafficBindings)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Exchange resource mutator failed", "error", err)
		os.Exit(2)
	}
	exchangeAPI, err := exchangeapi.New(
		stateStore, sessionAPI, serviceResolver, exchangeMutator,
		exchangeapi.Config{},
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Exchange Task API failed", "error", err)
		os.Exit(2)
	}
	mirrorMutator, err := mirrorapi.NewTrafficBindingResourceMutator(kubernetesProvider, stateStore, trafficBindings)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Mirror resource mutator failed", "error", err)
		os.Exit(2)
	}
	mirrorAPI, err := mirrorapi.New(
		stateStore, sessionAPI, serviceResolver, mirrorMutator,
		mirrorapi.Config{},
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Mirror Task API failed", "error", err)
		os.Exit(2)
	}
	previewResources, err := previewapi.NewTrafficBindingResourceManager(stateStore, trafficBindings)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Preview resource manager failed", "error", err)
		os.Exit(2)
	}
	previewAPI, err := previewapi.New(
		stateStore, sessionAPI, previewResources,
		previewapi.Config{},
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Preview Task API failed", "error", err)
		os.Exit(2)
	}
	if relayRegistry != nil {
		trafficDispatcher, dispatcherErr := trafficcontrolapi.NewDispatcher(exchangeAPI, mirrorAPI, previewAPI)
		if dispatcherErr != nil {
			_ = stateStore.Close()
			logger.Error("initialize traffic control dispatcher failed", "error", dispatcherErr)
			os.Exit(2)
		}
		trafficAPI, trafficErr := trafficcontrolapi.New(relayRegistry.authenticator, trafficDispatcher)
		if trafficErr == nil {
			trafficErr = relayRegistry.handler.Mount(trafficAPI)
		}
		if trafficErr != nil {
			_ = stateStore.Close()
			logger.Error("initialize Gateway traffic control API failed", "error", trafficErr)
			os.Exit(2)
		}
	}
	podExecutor, err := execapi.NewKubernetesExecutor(kubernetesProvider)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Kubernetes Pod executor failed", "error", err)
		os.Exit(2)
	}
	execAPI, err := execapi.New(stateStore, sessionAPI, podExecutor, execapi.Config{})
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Pod exec Task API failed", "error", err)
		os.Exit(2)
	}
	fileTargetResolver, err := controlplanekubernetes.NewContainerResolver(kubernetesProvider)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Kubernetes file target resolver failed", "error", err)
		os.Exit(2)
	}
	fileConfig, err := fileapi.ConfigFromEnv()
	if err != nil {
		_ = stateStore.Close()
		logger.Error("load file transfer configuration failed", "error", err)
		os.Exit(2)
	}
	fileExecutor, err := fileapi.NewKubernetesTransferExecutor(podExecutor, fileConfig.MaximumBytes)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Kubernetes file transfer executor failed", "error", err)
		os.Exit(2)
	}
	fileAPI, err := fileapi.New(stateStore, sessionAPI, fileTargetResolver, fileExecutor, fileConfig)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize file transfer Task API failed", "error", err)
		os.Exit(2)
	}
	fileOperator, err := fileopsapi.NewKubernetesOperator(podExecutor)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Kubernetes remote file operator failed", "error", err)
		os.Exit(2)
	}
	fileOperationsAPI, err := fileopsapi.New(stateStore, sessionAPI, fileTargetResolver, fileOperator, fileopsapi.Config{
		AllowedPathRoots: fileConfig.AllowedPathRoots,
	})
	if err != nil {
		_ = stateStore.Close()
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
	auditSink, err := controlplane.NewStorageAuditSink(stateStore.Audit())
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize API audit sink failed", "error", err)
		os.Exit(2)
	}
	methods := discoveryAuthMethods(authRegistry)
	if localUsers != nil {
		methods = append(methods, controlplane.AuthMethod{ID: "local", Type: "local", DisplayName: "Local account", Interaction: "browser"})
	}
	warnDevelopmentAuthentication(logger, authRegistry.Descriptors())
	readiness := health.CheckFunc(func(ctx context.Context) error {
		if err := stateStore.Check(ctx); err != nil {
			return err
		}
		if err := managementPolicyLoader.Check(ctx); err != nil {
			return err
		}
		if err := managedProviderRuntime.Check(ctx); err != nil {
			return err
		}
		if managementFile.BreakGlass.Enabled {
			if _, err := breakGlassStore.CurrentBreakGlassState(ctx); err != nil {
				return err
			}
		}
		if err := authRegistry.Check(ctx); err != nil {
			return err
		}
		return kubernetesProvider.Check(ctx)
	})
	authMethodSource := controlplane.AuthMethodSourceFunc(func() []controlplane.AuthMethod {
		result := discoveryAuthMethods(authRegistry)
		if localUsers != nil {
			result = append(result, controlplane.AuthMethod{ID: "local", Type: "local", DisplayName: "Local account", Interaction: "browser"})
		}
		return result
	})
	serverOptions := []controlplane.ServerOption{
		controlplane.WithReadinessChecker(readiness), controlplane.WithAuthorizer(policyEngine),
		controlplane.WithAuditSink(auditSink), controlplane.WithAPIRoutes(apiRoutes),
		controlplane.WithAuthMethodSource(authMethodSource),
	}
	managementSessions, err := adminsession.New(stateStore, breakGlassStore)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Session service failed", "error", err)
		os.Exit(2)
	}
	var managementRelayRuntimes []adminoperations.RelayRuntime
	if relayRegistry != nil {
		managementRelayRuntimes = append(managementRelayRuntimes, relayRegistry.registry)
	}
	managementOperations, err := adminoperations.New(stateStore, sessionRuntime, managementRelayRuntimes...)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Operations service failed", "error", err)
		os.Exit(2)
	}
	err = managementOperations.ConfigureRecovery(adminoperations.RecoveryRunnerFunc(func(ctx context.Context) (map[string]int, error) {
		counts := make(map[string]int, 2)
		var result error
		for name, run := range map[string]func(context.Context) (int, error){
			"session-runtime": sessionRecovery.RunOnce,
			"traffic-binding": bindingRecovery.RunOnce,
		} {
			count, runErr := run(ctx)
			counts[name] = count
			result = errors.Join(result, runErr)
		}
		return counts, result
	}))
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management recovery runner failed", "error", err)
		os.Exit(2)
	}
	var tokenService *token.Service
	var authRoutes controlplane.RouteRegistrar
	if len(authRegistry.Descriptors()) > 0 || len(managementFile.ProviderSecretAliases) > 0 || localUsers != nil {
		signingKey, err := token.LoadSigningKey(*tokenSigningKeyFile)
		if err != nil {
			_ = stateStore.Close()
			logger.Error("load token signing key failed", "error", err)
			os.Exit(2)
		}
		tokenService, err = token.New(stateStore, token.Config{
			Issuer: *publicURL, KeyID: *tokenKeyID, SigningKey: signingKey,
			AccessTTL: *accessTokenTTL, RefreshTTL: *refreshTokenTTL,
		})
		if err != nil {
			_ = stateStore.Close()
			logger.Error("initialize token service failed", "error", err)
			os.Exit(2)
		}
		loginService, err := login.New(authRegistry, stateStore, login.Config{
			Clients: []login.Client{
				{ID: login.DefaultDesktopClientID, AllowLoopback: true,
					Scopes: []string{"openid", "profile", "email", "offline_access", "kubeloop.api"}},
				{ID: "kubeloop-management",
					RedirectURIs: []string{strings.TrimRight(*managementPublicURL, "/") + controlplane.APIPathPrefix + "/admin/ui/callback"},
					Scopes:       []string{"openid", "profile", "email", "offline_access", "kubeloop.api"}},
			},
		})
		if err != nil {
			_ = stateStore.Close()
			logger.Error("initialize login service failed", "error", err)
			os.Exit(2)
		}
		var authOptions []httpauth.Option
		if localUsers != nil {
			authOptions = append(authOptions, httpauth.WithLocalAuthenticator(func(
				ctx context.Context, username string, password []byte, secondFactor, requestID string,
			) (controlplanestorage.Principal, error) {
				user, authErr := localUsers.Authenticate(ctx, username, password, secondFactor, requestID)
				if authErr != nil {
					return controlplanestorage.Principal{}, authErr
				}
				return stateStore.Principals().GetByID(ctx, user.PrincipalID)
			}))
		}
		authService, err := httpauth.New(loginService, tokenService, authOptions...)
		if err != nil {
			_ = stateStore.Close()
			logger.Error("initialize authentication HTTP handler failed", "error", err)
			os.Exit(2)
		}
		authRoutes = httpauth.NewRoutes(authService)
		serverOptions = append(serverOptions,
			controlplane.WithAuthRoutes(authRoutes),
			controlplane.WithAuthenticator(authenticateWithTokens(tokenService)),
		)
	}
	managementOptions := []adminhttpapi.Option{adminhttpapi.WithReadAPI(
		managementPolicyEngine, stateStore, adminhttpapi.BuildInfo{
			Version: version, Commit: commit, ProtocolMin: protocolMin, ProtocolMax: protocolMax,
		},
	), adminhttpapi.WithPolicyAPI(managementRevisionService, managementPolicyLoader),
		adminhttpapi.WithProviderAPI(managedProviderService),
		adminhttpapi.WithOperationsAPI(managementOperations)}
	if localUsers != nil {
		managementOptions = append(managementOptions, adminhttpapi.WithLocalUsers(localUsers))
	}
	if relayRegistry != nil {
		managementOptions = append(managementOptions, adminhttpapi.WithRelayStatusSource(relayRegistry.registry))
	}
	if tokenService != nil {
		managementOptions = append(managementOptions, adminhttpapi.WithTokenExchange(tokenService))
	}
	managementHandler, err := adminhttpapi.New(
		adminhttpapi.Config{PublicURL: *managementPublicURL}, managementSessions, managementOptions...,
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane HTTP API failed", "error", err)
		os.Exit(2)
	}
	managementServer, err := adminhttpserver.New(
		adminhttpserver.Config{ListenAddress: *managementListenAddress}, managementHandler,
		authRoutes, authMethodSource, logger,
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("invalid Management Plane server configuration", "error", err)
		os.Exit(2)
	}
	server, err := controlplane.NewServer(controlplane.Config{
		ListenAddress:       *listenAddress,
		PublicURL:           *publicURL,
		ServiceID:           *serviceID,
		TunnelPath:          *tunnelPath,
		MinClientVersion:    *minClientVersion,
		AuthMethods:         methods,
		ShutdownTimeout:     *shutdownTimeout,
		APIRequestTimeout:   *apiRequestTimeout,
		MaxRequestBodyBytes: *maxRequestBodyBytes,
	}, controlplane.BuildInfo{
		Version: version, Commit: commit, ProtocolMin: protocolMin, ProtocolMax: protocolMax,
	}, logger, serverOptions...)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("invalid control plane configuration", "error", err)
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", server.ListenAddress())
	if err != nil {
		_ = stateStore.Close()
		logger.Error("listen failed", "address", server.ListenAddress(), "error", err)
		os.Exit(1)
	}
	managementListener, err := net.Listen("tcp", managementServer.ListenAddress())
	if err != nil {
		_ = listener.Close()
		_ = stateStore.Close()
		logger.Error("Management Plane listen failed", "address", managementServer.ListenAddress(), "error", err)
		os.Exit(1)
	}
	var relayServer *http.Server
	var relayListener net.Listener
	if relayRegistry != nil {
		rawRelayListener, listenErr := net.Listen("tcp", relayRegistry.listenAddress)
		if listenErr != nil {
			_ = listener.Close()
			_ = managementListener.Close()
			_ = stateStore.Close()
			logger.Error("Relay Registry listen failed", "address", relayRegistry.listenAddress, "error", listenErr)
			os.Exit(1)
		}
		relayListener = tls.NewListener(rawRelayListener, relayRegistry.tlsConfig)
		relayServer = &http.Server{
			Handler: relayRegistry.handler, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
			MaxHeaderBytes: controlplane.DefaultMaxHeaderBytes,
		}
	}
	logger.Info("kubeloop control plane started",
		"version", version, "commit", commit,
		"protocol_min", protocolMin, "protocol_max", protocolMax,
		"listen_address", listener.Addr().String(), "public_url", *publicURL,
		"kubernetes_impersonation", kubernetesConfig.Impersonation.Enabled,
	)
	logger.Info("Management Plane started", "listen_address", managementListener.Addr().String(), "public_url", *managementPublicURL)
	if relayListener != nil {
		logger.Info("Relay Registry started", "listen_address", relayListener.Addr().String(), "transport", "mTLS")
	}
	go managedProviderRuntime.Run(signalContext)
	serveCount := 2
	if relayServer != nil {
		serveCount++
	}
	errCh := make(chan error, serveCount)
	var backgroundWorkers sync.WaitGroup
	backgroundWorkers.Add(5)
	go func() {
		defer backgroundWorkers.Done()
		managementPolicyLoader.Run(signalContext)
	}()
	go func() {
		defer backgroundWorkers.Done()
		sessionRecovery.Run(signalContext)
	}()
	go func() {
		defer backgroundWorkers.Done()
		managementOperations.Run(signalContext)
	}()
	go func() {
		defer backgroundWorkers.Done()
		maintenanceWorker.Run(signalContext)
	}()
	go func() {
		defer backgroundWorkers.Done()
		bindingRecovery.Run(signalContext)
	}()
	go func() { errCh <- server.Serve(listener) }()
	go func() { errCh <- managementServer.Serve(managementListener) }()
	if relayServer != nil {
		go func() {
			err := relayServer.Serve(relayListener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			errCh <- err
		}()
	}

	var firstServeError error
	serveResults := 0
	select {
	case err := <-errCh:
		serveResults++
		firstServeError = err
		if firstServeError == nil {
			firstServeError = errors.New("Control Plane listener stopped unexpectedly")
		}
		stop()
	case <-signalContext.Done():
	}
	logger.Info("kubeloop control plane shutting down")
	shutdownContext, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()
	shutdownError := server.Shutdown(shutdownContext)
	shutdownError = errors.Join(shutdownError, managementServer.Shutdown(shutdownContext))
	shutdownError = errors.Join(shutdownError, sessionRuntime.Shutdown(shutdownContext))
	if relayServer != nil {
		shutdownError = errors.Join(shutdownError, relayServer.Shutdown(shutdownContext))
	}
	for serveResults < serveCount {
		select {
		case serveError := <-errCh:
			serveResults++
			if serveError != nil && firstServeError == nil {
				firstServeError = serveError
			}
		case <-time.After(time.Second):
			logger.Warn("Control Plane serve loop did not report shutdown", "remaining", serveCount-serveResults)
			serveResults = serveCount
		}
	}
	backgroundWorkers.Wait()
	closeError := stateStore.Close()
	if finalError := errors.Join(firstServeError, shutdownError, closeError); finalError != nil {
		logger.Error("Control Plane stopped with an error", "error", finalError)
		os.Exit(1)
	}
}

func initializeLocalUsers(
	ctx context.Context,
	store *controlplanestorage.Store,
	issuer, usernameFile, passwordFile, mfaKeyFile string,
) (*adminlocaluser.Service, adminlocaluser.User, error) {
	usernameFile, passwordFile, mfaKeyFile = strings.TrimSpace(usernameFile), strings.TrimSpace(passwordFile), strings.TrimSpace(mfaKeyFile)
	if usernameFile == "" && passwordFile == "" && mfaKeyFile == "" {
		return nil, adminlocaluser.User{}, nil
	}
	if usernameFile == "" || passwordFile == "" || mfaKeyFile == "" {
		return nil, adminlocaluser.User{}, errors.New("all initial administrator Secret files are required")
	}
	username, err := readSecretFile(usernameFile, 256)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	defer clear(username)
	password, err := readSecretFile(passwordFile, 1024)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	defer clear(password)
	mfaKey, err := readBinarySecretFile(mfaKeyFile, 32)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	defer clear(mfaKey)
	service, err := adminlocaluser.New(store, mfaKey, issuer)
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	user, _, err := service.EnsureInitial(ctx, adminlocaluser.CreateRequest{
		Username: string(username), Password: password, DisplayName: "KubeLoop Administrator",
	})
	if err != nil {
		return nil, adminlocaluser.User{}, err
	}
	return service, user, nil
}

func readSecretFile(path string, maximum int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read Management Plane administrator Secret")
	}
	value := bytes.Clone(bytes.TrimSpace(raw))
	clear(raw)
	if len(value) == 0 || len(value) > maximum {
		clear(value)
		return nil, errors.New("Management Plane administrator Secret value is invalid")
	}
	return value, nil
}

func readBinarySecretFile(path string, exactLength int) ([]byte, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read Management Plane administrator Secret")
	}
	if len(value) != exactLength {
		clear(value)
		return nil, fmt.Errorf("Management Plane MFA key must contain exactly %d bytes", exactLength)
	}
	return value, nil
}

func ensureInitialAdminPolicy(ctx context.Context, service *adminrevision.Service, principalID string) error {
	if _, err := uuid.Parse(principalID); err != nil {
		return errors.New("initial administrator principal ID is invalid")
	}
	state, err := service.CurrentPolicy(ctx)
	if err != nil {
		return err
	}
	for _, assignment := range state.Snapshot.Assignments {
		if assignment.Role != adminauthorization.RolePlatformAdmin {
			continue
		}
		for _, subject := range assignment.Subjects {
			if subject == principalID {
				return nil
			}
		}
	}
	expectedETag := state.Pointer.ETag
	snapshot := state.Snapshot
	snapshot.Revision = 0
	snapshot.Assignments = append(snapshot.Assignments, adminauthorization.Assignment{
		ID:   uuid.NewSHA1(uuid.NameSpaceURL, []byte("kubeloop:helm-admin:"+principalID)).String(),
		Role: adminauthorization.RolePlatformAdmin, Subjects: []string{principalID},
	})
	idempotencyKey := fmt.Sprintf("initial-admin-policy-%s-%d", principalID, expectedETag)
	requestID := uuid.NewString()
	actor := adminrevision.Actor{PrincipalID: principalID, Authentication: adminauthorization.AuthenticationBootstrap}
	draft, err := service.CreatePolicyDraft(ctx, adminrevision.PolicyDraftRequest{
		Snapshot: snapshot, ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey, Reason: "initialize Helm administrator",
		RequestID: requestID, Actor: actor,
	})
	if err != nil {
		return err
	}
	_, err = service.PublishPolicy(ctx, adminrevision.ActivateRequest{
		ChangeID: draft.Change.ID, ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey,
		Reason: "initialize Helm administrator", RequestID: requestID, Actor: actor,
	})
	return err
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func discoveryAuthMethods(registry *authn.Registry) []controlplane.AuthMethod {
	descriptors := registry.Descriptors()
	methods := make([]controlplane.AuthMethod, 0, len(descriptors))
	for _, descriptor := range descriptors {
		methods = append(methods, controlplane.AuthMethod{
			ID: descriptor.ID, Type: string(descriptor.Type),
			DisplayName: descriptor.DisplayName, Interaction: string(descriptor.Interaction),
		})
	}
	return methods
}

func warnDevelopmentAuthentication(logger *slog.Logger, descriptors []authn.Descriptor) {
	for _, descriptor := range descriptors {
		switch descriptor.Type {
		case authn.ProviderAnonymous:
			logger.Warn("!!! SECURITY WARNING: ANONYMOUS DEVELOPMENT AUTHENTICATION IS ENABLED !!!",
				"provider_id", descriptor.ID, "production_safe", false)
		}
	}
}

func durationEnvOrDefault(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return parsed, nil
}

func authenticateWithTokens(tokenService *token.Service) controlplaneapi.AuthenticatorFunc {
	return func(request *http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
		headers := request.Header.Values("Authorization")
		if len(headers) != 1 {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required",
			}
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required",
			}
		}
		identity, err := tokenService.Authenticate(request.Context(), parts[1])
		if err != nil {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "access token is invalid",
			}
		}
		principal := identity.Principal
		return controlplaneapi.Principal{
			Subject: principal.ID, Provider: principal.Provider,
			DisplayName: principal.DisplayName, Email: principal.Email,
			Groups:   append([]string(nil), principal.Groups...),
			DeviceID: identity.DeviceID, FamilyID: identity.FamilyID,
			AccessExpiresAt: identity.AccessExpiresAt,
		}, nil
	}
}

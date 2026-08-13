package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	adminhttpapi "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpapi"
	adminhttpserver "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpserver"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/logging"
)

var (
	version     = "dev"
	commit      = "unknown"
	protocolMin = "2.0"
	protocolMax = "2.0"
)

func main() {
	configPath := flag.String("config", "", "Control Plane YAML configuration file")
	flag.Parse()

	environment, err := loadControlPlaneEnvironment()
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid Control Plane environment", "error", err)
		os.Exit(2)
	}
	config, err := loadControlPlaneConfig(*configPath)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid Control Plane configuration", "error", err)
		os.Exit(2)
	}
	parsedLogLevel, err := logging.ParseLevel(config.Document.Logging.Level)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("invalid log level", "error", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsedLogLevel}))
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bootstrap := bootstrapControlPlane(signalContext, config, logger)
	stateStore := bootstrap.Store
	maintenanceWorker := bootstrap.MaintenanceWorker
	localUsers := bootstrap.LocalUsers
	managementRevisionService := bootstrap.ManagementRevisionService
	managementPolicyEngine := bootstrap.ManagementPolicyEngine
	managementPolicyLoader := bootstrap.ManagementPolicyLoader
	authRegistry := bootstrap.AuthRegistry
	managedProviderRuntime := bootstrap.ManagedProviderRuntime
	managedProviderService := bootstrap.ManagedProviderService
	policyEngine := bootstrap.PolicyEngine
	kubernetesConfig := bootstrap.KubernetesConfig
	kubernetesProvider := bootstrap.KubernetesProvider
	apiRuntime := buildAPIRuntime(signalContext, config, environment, logger, stateStore, policyEngine, kubernetesProvider)
	apiRoutes := apiRuntime.Routes
	relayRegistry := apiRuntime.RelayRegistry
	sessionRuntime := apiRuntime.SessionRuntime
	sessionRecovery := apiRuntime.SessionRecovery
	bindingRecovery := apiRuntime.BindingRecovery
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
	managementSessions, err := adminsession.New(stateStore)
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
	if len(authRegistry.Descriptors()) > 0 || len(config.Management.ProviderSecretAliases) > 0 || localUsers != nil {
		signingKey, err := token.LoadSigningKey(config.Document.Authentication.Token.SigningKeyFile)
		if err != nil {
			_ = stateStore.Close()
			logger.Error("load token signing key failed", "error", err)
			os.Exit(2)
		}
		tokenService, err = token.New(stateStore, token.Config{
			Issuer: config.Document.API.PublicURL, KeyID: config.Document.Authentication.Token.KeyID, SigningKey: signingKey,
			AccessTTL: config.AccessTokenTTL, RefreshTTL: config.RefreshTokenTTL,
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
					RedirectURIs: []string{strings.TrimRight(config.Document.Management.PublicURL, "/") + controlplane.AdminAPIPathPrefix + "/ui/callback"},
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
		authRoutes = httpauth.NewRoutes(authService, httpauth.WithExistingSession(
			adminhttpapi.SessionCookieName,
			func(ctx context.Context, sessionToken string) (controlplanestorage.Principal, error) {
				stored, authErr := managementSessions.Authenticate(ctx, sessionToken)
				if authErr != nil || stored.PrincipalID == "" {
					return controlplanestorage.Principal{}, adminsession.ErrSessionInvalid
				}
				principal, authErr := stateStore.Principals().GetByID(ctx, stored.PrincipalID)
				if authErr != nil || principal.Provider != "local" {
					return controlplanestorage.Principal{}, adminsession.ErrSessionInvalid
				}
				return principal, nil
			},
		))
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
		adminhttpapi.Config{PublicURL: config.Document.Management.PublicURL}, managementSessions, managementOptions...,
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("initialize Management Plane HTTP API failed", "error", err)
		os.Exit(2)
	}
	managementServer, err := adminhttpserver.New(
		adminhttpserver.Config{ListenAddress: config.Document.Management.Listen}, managementHandler,
		authRoutes, authMethodSource, logger,
	)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("invalid Management Plane server configuration", "error", err)
		os.Exit(2)
	}
	server, err := controlplane.NewServer(controlplane.Config{
		ListenAddress:       config.Document.API.Listen,
		PublicURL:           config.Document.API.PublicURL,
		ServiceID:           config.Document.API.ServiceID,
		TunnelPath:          config.Document.API.TunnelPath,
		MinClientVersion:    config.Document.API.MinClientVersion,
		AuthMethods:         methods,
		ShutdownTimeout:     config.ShutdownTimeout,
		APIRequestTimeout:   config.APIRequestTimeout,
		MaxRequestBodyBytes: config.Document.API.MaxRequestBodyBytes,
	}, controlplane.BuildInfo{
		Version: version, Commit: commit, ProtocolMin: protocolMin, ProtocolMax: protocolMax,
	}, logger, serverOptions...)
	if err != nil {
		_ = stateStore.Close()
		logger.Error("invalid control plane configuration", "error", err)
		os.Exit(2)
	}
	serveControlPlane(serverRuntimeOptions{
		Context: signalContext, Stop: stop, Config: config, Logger: logger, Store: stateStore,
		Server: server, ManagementServer: managementServer, RelayRegistry: relayRegistry,
		KubernetesConfig: kubernetesConfig, ManagedProviderRuntime: managedProviderRuntime,
		ManagementPolicyLoader: managementPolicyLoader, SessionRecovery: sessionRecovery,
		ManagementOperations: managementOperations, MaintenanceWorker: maintenanceWorker,
		BindingRecovery: bindingRecovery, SessionRuntime: sessionRuntime,
	})
}

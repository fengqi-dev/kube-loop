package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	internalcli "github.com/fengqi-dev/kube-loop/internal/cli"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	adminhttpapi "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/httpapi"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitCode := executeControlPlane(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(exitCode)
}

func executeControlPlane(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return internalcli.Execute(ctx, newControlPlaneCommand(stdout), args, stdout, stderr)
}

func newControlPlaneCommand(stdout io.Writer) *cobra.Command {
	configResolver := newControlPlaneConfigResolver()
	command := &cobra.Command{
		Use:     "kubeloop-control-plane",
		Short:   "Run the KubeLoop control plane",
		Version: version,
		Args:    internalcli.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			environment := loadControlPlaneEnvironmentFrom(configResolver)
			configPath := controlPlaneOptionsFrom(configResolver)
			config, err := loadControlPlaneConfig(configPath)
			if err != nil {
				return internalcli.Usage(err)
			}
			config, err = applyControlPlaneOverrides(configResolver, config)
			if err != nil {
				return internalcli.Usage(err)
			}
			parsedLogLevel, err := logging.ParseLevel(config.Document.Logging.Level)
			if err != nil {
				return internalcli.Usage(fmt.Errorf("invalid log level: %w", err))
			}
			logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: parsedLogLevel}))
			runContext, stop := context.WithCancel(command.Context())
			defer stop()
			return runControlPlane(runContext, stop, config, environment, logger)
		},
	}
	internalcli.ConfigureRoot(command, "kubeloop-control-plane")
	internalcli.AddVersionCommand(command, "kubeloop-control-plane", version)
	command.Flags().String("config", "", "unified KubeLoop YAML configuration file")
	command.Flags().String("listen", "", "Control Plane API listen address")
	command.Flags().String("public-url", "", "public Control Plane URL")
	command.Flags().String("log-level", "", "log level: debug, info, warn, or error")
	bindings := map[string]string{
		"config": "control-plane.config-file", "listen": "control-plane.api.listen",
		"public-url": "control-plane.api.public-url", "log-level": "control-plane.logging.level",
	}
	for flagName, key := range bindings {
		if err := configResolver.BindPFlag(key, command.Flags().Lookup(flagName)); err != nil {
			panic(err)
		}
	}
	return command
}

func runControlPlane(
	signalContext context.Context,
	stop context.CancelFunc,
	config loadedControlPlaneConfig,
	environment controlPlaneEnvironment,
	logger *slog.Logger,
) (resultErr error) {
	bootstrap, err := bootstrapControlPlane(signalContext, config, logger)
	if err != nil {
		return err
	}
	stateStore := bootstrap.Store
	defer func() { resultErr = errors.Join(resultErr, stateStore.Close()) }()
	if err := controlplanestorage.EnsureBuiltinOAuthClients(
		signalContext,
		stateStore.OAuthClients(),
		strings.TrimRight(config.Document.API.PublicURL, "/")+controlplane.AdminPathPrefix+"/ui/callback",
	); err != nil {
		return fmt.Errorf("initialize built-in OAuth clients: %w", err)
	}
	maintenanceWorker := bootstrap.MaintenanceWorker
	localUsers := bootstrap.LocalUsers
	iamBootstrap := bootstrap.IAMBootstrap
	authorizer := bootstrap.Authorizer
	kubernetesConfig := bootstrap.KubernetesConfig
	kubernetesProvider := bootstrap.KubernetesProvider
	apiRuntime, err := buildAPIRuntime(
		signalContext,
		config,
		environment,
		logger,
		stateStore,
		authorizer,
		kubernetesProvider,
	)
	if err != nil {
		return err
	}
	apiRoutes := apiRuntime.Routes
	relayRegistry := apiRuntime.RelayRegistry
	sessionRuntime := apiRuntime.SessionRuntime
	sessionRecovery := apiRuntime.SessionRecovery
	bindingRecovery := apiRuntime.BindingRecovery
	auditSink, err := controlplane.NewStorageAuditSink(stateStore.Audit())
	if err != nil {
		return fmt.Errorf("initialize API audit sink: %w", err)
	}
	methods := []controlplane.AuthMethod{{
		ID: "local", Type: "local", DisplayName: "Local account", Interaction: "browser",
	}}
	readiness := health.CheckFunc(func(ctx context.Context) error {
		if err := stateStore.Check(ctx); err != nil {
			return err
		}
		return kubernetesProvider.Check(ctx)
	})
	authMethodSource := controlplane.AuthMethodSourceFunc(func() []controlplane.AuthMethod {
		return append([]controlplane.AuthMethod(nil), methods...)
	})
	serverOptions := []controlplane.ServerOption{
		controlplane.WithReadinessChecker(readiness), controlplane.WithAuthorizer(authorizer),
		controlplane.WithAuditSink(auditSink), controlplane.WithAPIRoutes(apiRoutes),
		controlplane.WithAuthMethodSource(authMethodSource),
	}
	managementSessions, err := adminsession.New(stateStore)
	if err != nil {
		return fmt.Errorf("initialize Management Session service: %w", err)
	}
	var authRoutes controlplane.RouteRegistrar
	var fositeEndpoints *oauthserver.Endpoints
	if localUsers != nil {
		fositeStorage, err := oauthserver.NewStorage(stateStore)
		if err != nil {
			return fmt.Errorf("initialize Fosite storage: %w", err)
		}
		oidcSigningKey, err := oauthserver.LoadSigningKey(config.Document.Authentication.OAuth.OIDCSigningKeyFile)
		if err != nil {
			return fmt.Errorf("load OIDC signing key: %w", err)
		}
		hmacSecret, err := oauthserver.LoadHMACSecret(config.Document.Authentication.OAuth.HMACSecretFile)
		if err != nil {
			return fmt.Errorf("load Fosite HMAC secret: %w", err)
		}
		fositeProvider, err := oauthserver.NewProvider(fositeStorage, oauthserver.Config{
			Issuer: config.Document.API.PublicURL, HMACSecret: hmacSecret, SigningKey: oidcSigningKey,
			AccessTokenTTL: config.AccessTokenTTL, RefreshTokenTTL: config.RefreshTokenTTL,
		})
		if err != nil {
			return fmt.Errorf("initialize Fosite provider: %w", err)
		}
		fositeEndpoints, err = oauthserver.NewEndpoints(
			fositeProvider,
			stateStore,
			config.Document.Authentication.OAuth.KeyID,
			oidcSigningKey,
		)
		if err != nil {
			return fmt.Errorf("initialize Fosite endpoints: %w", err)
		}
		if localUsers != nil {
			fositeEndpoints.SetLocalAuthenticator(func(
				ctx context.Context,
				username string,
				password []byte,
				requestID string,
			) (controlplanestorage.Identity, error) {
				user, authErr := localUsers.Authenticate(ctx, username, password, requestID)
				if authErr != nil {
					return controlplanestorage.Identity{}, authErr
				}
				return stateStore.Identities().GetByID(ctx, user.IdentityID)
			})
		}
		authRoutes = httpauth.NewRoutes(fositeEndpoints, httpauth.WithIssuer(config.Document.API.PublicURL))
		serverOptions = append(serverOptions,
			controlplane.WithAuthRoutes(authRoutes),
			controlplane.WithAuthenticator(authenticateWithFosite(fositeEndpoints)),
		)
	}
	managementOptions := []adminhttpapi.Option{adminhttpapi.WithReadAPI(stateStore),
		adminhttpapi.WithBootstrap(iamBootstrap),
		adminhttpapi.WithOAuthClients(stateStore, stateStore)}
	if localUsers != nil {
		managementOptions = append(managementOptions, adminhttpapi.WithLocalUsers(localUsers))
	}
	if authRoutes != nil {
		managementOptions = append(managementOptions, adminhttpapi.WithTokenExchange(fositeEndpoints))
	}
	managementHandler, err := adminhttpapi.New(
		adminhttpapi.Config{PublicURL: config.Document.API.PublicURL}, managementSessions, managementOptions...,
	)
	if err != nil {
		return fmt.Errorf("initialize Management Plane HTTP API: %w", err)
	}
	serverOptions = append(serverOptions, controlplane.WithAdminRoutes(managementHandler))
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
		return fmt.Errorf("invalid control plane configuration: %w", err)
	}
	if err := serveControlPlane(serverRuntimeOptions{
		Context: signalContext, Stop: stop, Config: config, Logger: logger,
		Server: server, RelayRegistry: relayRegistry,
		KubernetesConfig:  kubernetesConfig,
		SessionRecovery:   sessionRecovery,
		MaintenanceWorker: maintenanceWorker,
		BindingRecovery:   bindingRecovery, SessionRuntime: sessionRuntime,
	}); err != nil {
		return err
	}
	stop()
	return nil
}

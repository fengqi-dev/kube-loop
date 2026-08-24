package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	internalcli "github.com/fengqi-dev/kube-loop/internal/cli"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/operations"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/fengqi-dev/kube-loop/internal/logging"
)

var version = "dev"

func main() {
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitCode := executeGateway(signalContext, os.Args[1:], os.Stdout, os.Stderr)
	stopSignals()
	os.Exit(exitCode)
}

func executeGateway(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return internalcli.Execute(ctx, newGatewayCommand(ctx, stdout), args, stdout, stderr)
}

func newGatewayCommand(ctx context.Context, stdout io.Writer) *cobra.Command {
	configResolver := newGatewayConfigResolver()
	command := &cobra.Command{
		Use:     "kubeloop-gateway",
		Short:   "Run the KubeLoop tunnel data plane",
		Version: version,
		Args:    internalcli.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			environment, err := loadGatewayEnvironmentFrom(configResolver)
			if err != nil {
				return internalcli.Usage(err)
			}
			config, err := loadGatewayConfig(environment.ConfigFile)
			if err != nil {
				return internalcli.Usage(err)
			}
			config, err = applyGatewayOverrides(configResolver, config)
			if err != nil {
				return internalcli.Usage(err)
			}
			return runGateway(ctx, environment, config, stdout)
		},
	}
	internalcli.ConfigureRoot(command, "kubeloop-gateway")
	internalcli.AddVersionCommand(command, "kubeloop-gateway", version)
	command.Flags().String("config", "", "unified KubeLoop YAML configuration file")
	command.Flags().String("listen", "", "Gateway HTTP listen address")
	command.Flags().String("relay-control-plane-url", "", "Relay Registry control plane URL")
	command.Flags().String("relay-endpoint", "", "advertised Relay endpoint")
	command.Flags().String("log-level", "", "log level: debug, info, warn, or error")
	bindings := map[string]string{
		"config": "gateway.config-file", "listen": "gateway.http.listen",
		"relay-control-plane-url": "gateway.relay.control-plane-url",
		"relay-endpoint":          "gateway.relay.endpoint", "log-level": "gateway.log-level",
	}
	for flagName, key := range bindings {
		if err := configResolver.BindPFlag(key, command.Flags().Lookup(flagName)); err != nil {
			panic(err)
		}
	}
	return command
}

func applyGatewayOverrides(config *viper.Viper, loaded gatewayConfig) (gatewayConfig, error) {
	config.SetDefault("gateway.http.listen", loaded.HTTP.Listen)
	config.SetDefault("gateway.relay.control-plane-url", loaded.Relay.ControlPlaneURL)
	config.SetDefault("gateway.relay.endpoint", loaded.Relay.Endpoint)
	config.SetDefault("gateway.log-level", loaded.LogLevel)
	loaded.HTTP.Listen = config.GetString("gateway.http.listen")
	loaded.Relay.ControlPlaneURL = config.GetString("gateway.relay.control-plane-url")
	loaded.Relay.Endpoint = config.GetString("gateway.relay.endpoint")
	loaded.LogLevel = config.GetString("gateway.log-level")
	if err := loaded.normalizeAndValidate(); err != nil {
		return gatewayConfig{}, err
	}
	return loaded, nil
}

func runGateway(
	ctx context.Context,
	environment gatewayEnvironment,
	config gatewayConfig,
	stdout io.Writer,
) (resultErr error) {
	parsedLogLevel, err := logging.ParseLevel(config.LogLevel)
	if err != nil {
		return fmt.Errorf("parse log level: %w", err)
	}
	logHandler := logging.WithContext(
		slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: parsedLogLevel}),
	)
	componentLogger := slog.New(logHandler).With("component", "data-plane")
	logger := slog.NewLogLogger(componentLogger.Handler(), slog.LevelInfo)
	server := gateway.NewServer(componentLogger, 10*time.Second)
	var httpHandler *websocketmux.Handler
	var controlAgent *relayagent.Agent
	var runtimeReporter *relayRuntimeReporter
	var dynamicAuthenticator *relayagent.TicketAuthenticator
	var authenticatorErr error
	dynamicAuthenticator, authenticatorErr = relayagent.NewTicketAuthenticator(relayagent.TicketAuthenticatorConfig{
		RequiredOperation: "tunnel", ReplayEntries: config.Relay.ReplayEntries,
	})
	if authenticatorErr != nil {
		return fmt.Errorf("create RelayTicket authenticator: %w", authenticatorErr)
	}
	handler, handlerErr := websocketmux.NewHandler(websocketmux.ServerConfig{
		Authenticator: websocketmux.AuthenticatorFunc(func(request *http.Request) (websocketmux.Identity, error) {
			claims, verifyErr := dynamicAuthenticator.Verify(request)
			if verifyErr != nil {
				return websocketmux.Identity{}, verifyErr
			}
			if len(claims.NetworkSpecHash) != 64 {
				return websocketmux.Identity{}, errors.New("RelayTicket NetworkSpec binding is required")
			}
			return websocketmux.Identity{
				IdentityID: claims.IdentityID, Groups: append([]string(nil), claims.Groups...),
				DeviceID: claims.DeviceID, SessionID: claims.SessionID,
				SessionGeneration: claims.SessionGeneration,
				TicketID:          claims.TicketID,
				Namespace:         claims.Namespace, NetworkSpecHash: claims.NetworkSpecHash,
				ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
			}, nil
		}),
		Logger: componentLogger, StreamIdleTimeout: config.WebSocket.StreamIdleTimeout.Duration,
		HandshakeTimeout: config.WebSocket.HandshakeTimeout.Duration,
		ServerVersion:    version, MinClientVersion: config.MinClientVersion,
		MaxSessions: config.WebSocket.MaxSessions, MaxSessionsPerUser: config.WebSocket.MaxSessionsPerUser,
		MaxStreamsPerSession: config.WebSocket.MaxStreamsPerSession, MaxFrameBytes: config.WebSocket.MaxFrameBytes,
		Handle: func(ctx context.Context, identity websocketmux.Identity, connection net.Conn) {
			server.ServeConnForAuthorizationContext(ctx, connection, gateway.SessionAuthorization{
				RequestID: identity.RequestID, IdentityID: identity.IdentityID,
				Groups: append([]string(nil), identity.Groups...), DeviceID: identity.DeviceID,
				SessionID: identity.SessionID, Generation: identity.SessionGeneration,
				TicketID:        identity.TicketID,
				Namespace:       identity.Namespace,
				NetworkSpecHash: identity.NetworkSpecHash,
			})
		},
	})
	if handlerErr != nil {
		return fmt.Errorf("create WebSocket handler: %w", handlerErr)
	}
	httpHandler = handler
	operationsState := operationsGatewayState{gateway: server}
	advertisedEndpoint, endpointErr := expandRelayEndpoint(config.Relay.Endpoint, environment)
	if endpointErr != nil {
		return fmt.Errorf("expand Relay endpoint: %w", endpointErr)
	}
	httpClient, clientErr := relayagent.NewHTTPClient(relayagent.ClientTLSConfig{
		CertificateFile: config.Relay.ClientCertificateFile, PrivateKeyFile: config.Relay.ClientPrivateKeyFile,
		ServerCAFile: config.Relay.ServerCAFile, ServerName: config.Relay.ServerName,
	})
	if clientErr != nil {
		return fmt.Errorf("create Relay Registry HTTP client: %w", clientErr)
	}
	runtimeReporter = &relayRuntimeReporter{
		gateway: server, websocket: handler,
		//nolint:gosec // Gateway configuration bounds physical sessions to 1<<20.
		maximumPhysical: uint32(config.WebSocket.MaxSessions),
		//nolint:gosec // Gateway configuration bounds the validated product to 1<<24.
		maximumLogical: uint32(
			uint64(config.WebSocket.MaxSessions) * uint64(config.WebSocket.MaxStreamsPerSession),
		),
	}
	var agentErr error
	controlAgent, agentErr = relayagent.New(relayagent.Config{
		ControlPlaneURL: config.Relay.ControlPlaneURL, Endpoint: advertisedEndpoint, HTTPClient: httpClient,
		BearerTokenFile: config.Relay.BearerTokenFile,
		Reporter:        runtimeReporter, Applier: dynamicAuthenticator, Logger: logger,
	})
	if agentErr != nil {
		return fmt.Errorf("create Relay agent: %w", agentErr)
	}
	if agentErr := controlAgent.Start(ctx); agentErr != nil {
		return fmt.Errorf("start Relay agent: %w", agentErr)
	}
	defer func() {
		stopContext, cancelStop := context.WithTimeout(
			context.WithoutCancel(ctx),
			5*time.Second,
		)
		resultErr = errors.Join(resultErr, stopGatewayAgent(stopContext, controlAgent))
		cancelStop()
	}()
	operationsState.agent = controlAgent
	logger.Printf("Data Plane registered as %s", controlAgent.RelayID())
	trafficHandler, trafficErr := trafficapi.New(trafficapi.Config{
		GatewayIP: environment.PodIP, ControlPlane: controlAgent,
	})
	if trafficErr != nil {
		return fmt.Errorf("create traffic handler: %w", trafficErr)
	}
	server.SetTrafficHandler(trafficHandler)
	httpListener, listenErr := (&net.ListenConfig{}).Listen(ctx, "tcp", config.HTTP.Listen)
	if listenErr != nil {
		return fmt.Errorf("listen on %s: %w", config.HTTP.Listen, listenErr)
	}
	router := echo.New()
	router.Use(httpmiddleware.RequestID())
	router.Use(httpmiddleware.RequestLogger(componentLogger))
	operations.NewHandler(operationsState, handler).Register(router)
	router.Any(config.HTTP.Path, echo.WrapHandler(handler))
	defaultHTTPErrorHandler := echo.DefaultHTTPErrorHandler(false)
	router.HTTPErrorHandler = func(ctx *echo.Context, err error) {
		if !errors.Is(err, echo.ErrNotFound) {
			if errors.Is(err, echo.ErrMethodNotAllowed) {
				allow := strings.ReplaceAll(ctx.Response().Header().Get(echo.HeaderAllow), http.MethodOptions+", ", "")
				ctx.Response().Header().Set(echo.HeaderAllow, allow)
			}
			defaultHTTPErrorHandler(ctx, err)
			return
		}
		http.NotFound(ctx.Response(), ctx.Request())
	}
	return serveGateway(gatewayRuntimeOptions{
		Context: ctx, Logger: logger, ListenAddress: config.HTTP.Listen, Path: config.HTTP.Path,
		Listener: httpListener, Handler: router, Gateway: server, Admissions: httpHandler,
		Control: controlAgent, DrainTimeout: config.DrainTimeout.Duration,
		ServeStopTimeout: 5 * time.Second, Serve: websocketmux.Serve,
	})
}

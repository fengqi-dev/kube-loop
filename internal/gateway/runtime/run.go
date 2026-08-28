package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	options "github.com/fengqi-dev/kube-loop/internal/gateway/config"
	"github.com/fengqi-dev/kube-loop/internal/gateway/operations"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/logging"
	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

func Run(
	ctx context.Context,
	environment options.Environment,
	config options.Config,
	info buildinfo.Info,
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
	encryptionEnabled := config.WebSocket.TrafficEncryption == nil ||
		*config.WebSocket.TrafficEncryption
	var noiseStaticKey *trafficstream.NoiseStaticKeypair
	var noisePublicKey string
	if encryptionEnabled {
		generatedKey, generateErr := trafficstream.GenerateNoiseStaticKeypair()
		err = generateErr
		if err != nil {
			return fmt.Errorf("generate Gateway Noise static key: %w", err)
		}
		noiseStaticKey = &generatedKey
		noisePublicKey, err = trafficstream.EncodeNoisePublicKey(noiseStaticKey.Public)
		if err != nil {
			return fmt.Errorf("encode Gateway Noise public key: %w", err)
		}
	}
	var httpHandler *websocketmux.Handler
	var controlAgent *relayagent.Agent
	var runtimeReporter *Reporter
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
				ExpiresAt:         time.Unix(claims.ExpiresAt, 0).UTC(),
				TrafficEncryption: cloneBoolPointer(claims.TrafficEncryption),
				NoisePublicKey:    claims.NoisePublicKey,
			}, nil
		}),
		Logger: componentLogger, StreamIdleTimeout: config.WebSocket.StreamIdleTimeout.Duration,
		HandshakeTimeout: config.WebSocket.HandshakeTimeout.Duration,
		ServerVersion:    info.Version, MinClientVersion: config.MinClientVersion,
		MaxSessions: config.WebSocket.MaxSessions, MaxSessionsPerUser: config.WebSocket.MaxSessionsPerUser,
		MaxStreamsPerSession: config.WebSocket.MaxStreamsPerSession, MaxFrameBytes: config.WebSocket.MaxFrameBytes,
		TrafficEncryption: config.WebSocket.TrafficEncryption,
		NoisePublicKey:    noisePublicKey,
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
	operationsState := OperationsState{Gateway: server}
	advertisedEndpoint, endpointErr := options.ExpandRelayEndpoint(config.Relay.Endpoint, environment)
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
	runtimeReporter = &Reporter{
		Gateway: server, WebSocket: handler,
		//nolint:gosec // Gateway configuration bounds physical sessions to 1<<20.
		MaximumPhysical: uint32(config.WebSocket.MaxSessions),
		//nolint:gosec // Gateway configuration bounds the validated product to 1<<24.
		MaximumLogical: uint32(
			uint64(config.WebSocket.MaxSessions) * uint64(config.WebSocket.MaxStreamsPerSession),
		),
	}
	var agentErr error
	controlAgent, agentErr = relayagent.New(relayagent.Config{
		ControlPlaneURL: config.Relay.ControlPlaneURL, Endpoint: advertisedEndpoint, HTTPClient: httpClient,
		BearerTokenFile: config.Relay.BearerTokenFile,
		Reporter:        runtimeReporter, Applier: dynamicAuthenticator, Logger: logger,
		TrafficEncryption: encryptionEnabled, NoisePublicKey: noisePublicKey,
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
	operationsState.Agent = controlAgent
	logger.Printf("Data Plane registered as %s", controlAgent.RelayID())
	trafficHandler, trafficErr := trafficapi.New(trafficapi.Config{
		GatewayIP: environment.PodIP, ControlPlane: controlAgent,
		TrafficEncryption: config.WebSocket.TrafficEncryption,
		NoiseStaticKey:    noiseStaticKey,
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
	router.Use(middleware.RequestID())
	router.Use(middleware.RequestLogger(componentLogger))
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

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

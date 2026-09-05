package gateway

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
	"github.com/fengqi-dev/kube-loop/internal/gateway/api"
	options "github.com/fengqi-dev/kube-loop/internal/gateway/config"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/agent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trojanproxy"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trojanruntime"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/logging"
	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
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
	server := NewServer(componentLogger)
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
	var controlAgent *agent.Agent
	var runtimeReporter *Reporter
	var dynamicAuthenticator *agent.TicketAuthenticator
	var authenticatorErr error
	dynamicAuthenticator, authenticatorErr = agent.NewTicketAuthenticator(agent.TicketAuthenticatorConfig{
		RequiredOperation: "tunnel", ReplayEntries: config.Relay.ReplayEntries,
	})
	if authenticatorErr != nil {
		return fmt.Errorf("create RelayTicket authenticator: %w", authenticatorErr)
	}
	ticketAuthenticator := newGatewayAuthenticator(dynamicAuthenticator.Verify)
	forwardAuthenticator := newGatewayAuthenticator(dynamicAuthenticator.VerifyReusable)
	handler, handlerErr := websocketmux.NewHandler(websocketmux.ServerConfig{
		Authenticator: ticketAuthenticator,
		Logger:        componentLogger, StreamIdleTimeout: config.WebSocket.StreamIdleTimeout.Duration,
		HandshakeTimeout: config.WebSocket.HandshakeTimeout.Duration,
		ServerVersion:    info.Version, MinClientVersion: config.MinClientVersion,
		MaxSessions: config.WebSocket.MaxSessions, MaxSessionsPerUser: config.WebSocket.MaxSessionsPerUser,
		MaxStreamsPerSession: config.WebSocket.MaxStreamsPerSession, MaxFrameBytes: config.WebSocket.MaxFrameBytes,
		TrafficEncryption: config.WebSocket.TrafficEncryption,
		NoisePublicKey:    noisePublicKey,
		Handle: func(ctx context.Context, identity websocketmux.Identity, connection net.Conn) {
			server.ServeConnForAuthorizationContext(ctx, connection, SessionAuthorization{
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
	var forwardHandler http.Handler
	var forwardAdmissions *trojanproxy.Handler
	if config.Forward.Enabled {
		forwardRuntime, runtimeErr := trojanruntime.NewManager(ctx, trojanruntime.Config{
			BinaryPath: config.Forward.SingBoxPath, LogLevel: config.LogLevel, Logger: componentLogger,
		})
		if runtimeErr != nil {
			return fmt.Errorf("create Gateway forward runtime: %w", runtimeErr)
		}
		defer func() { resultErr = errors.Join(resultErr, forwardRuntime.Close()) }()
		server.Forward = forwardRuntime
		proxyHandler, proxyErr := trojanproxy.NewHandler(trojanproxy.Config{
			Path: config.Forward.Path, Authenticator: forwardAuthenticator,
			Resolver: forwardRuntime, Logger: componentLogger,
			MaxSessions: config.WebSocket.MaxSessions,
		})
		if proxyErr != nil {
			return fmt.Errorf("create Gateway Trojan WebSocket handler: %w", proxyErr)
		}
		forwardHandler = proxyHandler
		forwardAdmissions = proxyHandler
	}
	operationsState := OperationsState{Gateway: server}
	advertisedEndpoint, endpointErr := options.ExpandRelayEndpoint(config.Relay.Endpoint, environment)
	if endpointErr != nil {
		return fmt.Errorf("expand Relay endpoint: %w", endpointErr)
	}
	httpClient, clientErr := agent.NewHTTPClient(agent.ClientTLSConfig{
		CertificateFile: config.Relay.ClientCertificateFile, PrivateKeyFile: config.Relay.ClientPrivateKeyFile,
		ServerCAFile: config.Relay.ServerCAFile, ServerName: config.Relay.ServerName,
	})
	if clientErr != nil {
		return fmt.Errorf("create Relay Registry HTTP client: %w", clientErr)
	}
	maximumPhysical := config.WebSocket.MaxSessions
	if config.Forward.Enabled {
		maximumPhysical += config.WebSocket.MaxSessions
	}
	runtimeReporter = &Reporter{
		Gateway: server, WebSocket: handler, Forward: forwardAdmissions,
		//nolint:gosec // At most two bounded transports each admit MaxSessions (<= 1<<20).
		MaximumPhysical: uint32(maximumPhysical),
		//nolint:gosec // Gateway configuration bounds the validated product to 1<<24.
		MaximumLogical: uint32(
			uint64(config.WebSocket.MaxSessions) * uint64(config.WebSocket.MaxStreamsPerSession),
		),
	}
	var agentErr error
	controlAgent, agentErr = agent.New(agent.Config{
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
	trafficHandler, trafficErr := api.New(api.Config{
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
	api.NewHandler(operationsState, handler).Register(router)
	var tunnelHandler http.Handler = handler
	if forwardHandler != nil && config.Forward.Path == config.HTTP.Path {
		tunnelHandler = NewTunnelHandler(handler, forwardHandler)
	}
	router.Any(config.HTTP.Path, echo.WrapHandler(tunnelHandler))
	if forwardHandler != nil && config.Forward.Path != config.HTTP.Path {
		router.Any(config.Forward.Path, echo.WrapHandler(forwardHandler))
	}
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
		Listener: httpListener, Handler: router, Gateway: server,
		Control: controlAgent, DrainTimeout: config.DrainTimeout.Duration,
		ServeStopTimeout: 5 * time.Second, Serve: websocketmux.Serve,
		Admissions: admissionGroup{httpHandler, forwardAdmissions},
	})
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func newGatewayAuthenticator(
	verify func(*http.Request) (relayticket.Claims, error),
) websocketmux.Authenticator {
	return websocketmux.AuthenticatorFunc(func(request *http.Request) (websocketmux.Identity, error) {
		claims, err := verify(request)
		if err != nil {
			return websocketmux.Identity{}, err
		}
		if len(claims.NetworkSpecHash) != 64 {
			return websocketmux.Identity{}, errors.New("RelayTicket NetworkSpec binding is required")
		}
		return websocketmux.Identity{
			IdentityID: claims.IdentityID, Groups: append([]string(nil), claims.Groups...),
			DeviceID: claims.DeviceID, SessionID: claims.SessionID,
			SessionGeneration: claims.SessionGeneration, TicketID: claims.TicketID,
			Namespace: claims.Namespace, NetworkSpecHash: claims.NetworkSpecHash,
			ExpiresAt:         time.Unix(claims.ExpiresAt, 0).UTC(),
			TrafficEncryption: cloneBoolPointer(claims.TrafficEncryption), NoisePublicKey: claims.NoisePublicKey,
		}, nil
	})
}

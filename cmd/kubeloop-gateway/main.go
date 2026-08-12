package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/operations"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/fengqi-dev/kube-loop/internal/logging"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/labstack/echo/v5"
)

var version = "dev"

func main() {
	config, err := loadGatewayConfig(os.Getenv(gatewayConfigFileEnvironment))
	if err != nil {
		log.Fatal(err)
	}
	parsedLogLevel, err := logging.ParseLevel(config.LogLevel)
	if err != nil {
		log.Fatal(err)
	}
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsedLogLevel})
	componentLogger := slog.New(logHandler).With("component", "data-plane")
	logger := slog.NewLogLogger(componentLogger.Handler(), slog.LevelInfo)
	errorLogger := slog.NewLogLogger(componentLogger.Handler(), slog.LevelError)
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	server := gateway.NewServer(componentLogger, 10*time.Second)
	errCh := make(chan error, 1)
	var httpHandler *websocketmux.Handler
	var cancelHTTP context.CancelFunc
	var controlAgent *relayagent.Agent
	var runtimeReporter *relayRuntimeReporter
	var verifyRequest func(*http.Request) (relayticket.Claims, error)

	var dynamicAuthenticator *relayagent.TicketAuthenticator
	var authenticatorErr error
	dynamicAuthenticator, authenticatorErr = relayagent.NewTicketAuthenticator(relayagent.TicketAuthenticatorConfig{
		RequiredOperation: "tunnel", ReplayEntries: config.Relay.ReplayEntries,
	})
	if authenticatorErr != nil {
		errorLogger.Fatal(authenticatorErr)
	}
	verifyRequest = dynamicAuthenticator.Verify
	handler, handlerErr := websocketmux.NewHandler(websocketmux.ServerConfig{
		Authenticator: websocketmux.AuthenticatorFunc(func(request *http.Request) (websocketmux.Identity, error) {
			claims, verifyErr := verifyRequest(request)
			if verifyErr != nil {
				return websocketmux.Identity{}, verifyErr
			}
			if len(claims.NetworkSpecHash) != 64 {
				return websocketmux.Identity{}, errors.New("RelayTicket NetworkSpec binding is required")
			}
			return websocketmux.Identity{
				PrincipalID: claims.PrincipalID, DeviceID: claims.DeviceID, SessionID: claims.SessionID,
				SessionGeneration: claims.SessionGeneration,
				Namespace:         claims.Namespace, NetworkSpecHash: claims.NetworkSpecHash,
				ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
			}, nil
		}),
		Logger: componentLogger, StreamIdleTimeout: config.WebSocket.StreamIdleTimeout.Duration,
		HandshakeTimeout: config.WebSocket.HandshakeTimeout.Duration,
		ServerVersion:    version, MinClientVersion: config.MinClientVersion,
		MaxSessions: config.WebSocket.MaxSessions, MaxSessionsPerUser: config.WebSocket.MaxSessionsPerUser,
		MaxStreamsPerSession: config.WebSocket.MaxStreamsPerSession, MaxFrameBytes: config.WebSocket.MaxFrameBytes,
		Handle: func(identity websocketmux.Identity, connection net.Conn) {
			server.ServeConnForAuthorization(connection, gateway.SessionAuthorization{
				RequestID: identity.RequestID, SessionID: identity.SessionID, Generation: identity.SessionGeneration,
				Namespace:       identity.Namespace,
				NetworkSpecHash: identity.NetworkSpecHash,
			})
		},
	})
	if handlerErr != nil {
		errorLogger.Fatal(handlerErr)
	}
	httpHandler = handler
	operationsState := operationsGatewayState{gateway: server}
	advertisedEndpoint, endpointErr := expandRelayEndpoint(config.Relay.Endpoint)
	if endpointErr != nil {
		errorLogger.Fatal(endpointErr)
	}
	httpClient, clientErr := relayagent.NewHTTPClient(relayagent.ClientTLSConfig{
		CertificateFile: config.Relay.ClientCertificateFile, PrivateKeyFile: config.Relay.ClientPrivateKeyFile,
		ServerCAFile: config.Relay.ServerCAFile, ServerName: config.Relay.ServerName,
	})
	if clientErr != nil {
		errorLogger.Fatal(clientErr)
	}
	runtimeReporter = &relayRuntimeReporter{
		gateway: server, websocket: handler, maximumPhysical: uint32(config.WebSocket.MaxSessions),
		maximumLogical: uint32(uint64(config.WebSocket.MaxSessions) * uint64(config.WebSocket.MaxStreamsPerSession)),
	}
	var agentErr error
	controlAgent, agentErr = relayagent.New(relayagent.Config{
		ControlPlaneURL: config.Relay.ControlPlaneURL, Endpoint: advertisedEndpoint, HTTPClient: httpClient,
		BearerTokenFile: config.Relay.BearerTokenFile,
		Reporter:        runtimeReporter, Applier: dynamicAuthenticator, Logger: logger,
	})
	if agentErr != nil {
		errorLogger.Fatal(agentErr)
	}
	if agentErr := controlAgent.Start(signalContext); agentErr != nil {
		errorLogger.Fatal(agentErr)
	}
	operationsState.agent = controlAgent
	logger.Printf("Data Plane registered as %s", controlAgent.RelayID())
	httpListener, listenErr := net.Listen("tcp", config.HTTP.Listen)
	if listenErr != nil {
		errorLogger.Fatal(listenErr)
	}
	router := echo.New()
	router.Use(httpmiddleware.RequestID())
	router.Use(httpmiddleware.RequestLogger(componentLogger))
	operations.NewHandler(operationsState, handler).Register(router)
	router.Any(config.HTTP.Path, echo.WrapHandler(handler))
	trafficHandler, trafficErr := trafficapi.New(trafficapi.Config{
		GatewayIP: strings.TrimSpace(os.Getenv("KUBELOOP_POD_IP")), VerifyRequest: verifyRequest, ControlPlane: controlAgent,
		MaximumSessions: config.WebSocket.MaxSessions, OtherSessions: handler.ActiveSessions,
	})
	if trafficErr != nil {
		errorLogger.Fatal(trafficErr)
	}
	trafficHandler.RegisterRoutes(router)
	runtimeReporter.SetTraffic(trafficHandler)
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
	httpContext, cancel := context.WithCancel(context.Background())
	cancelHTTP = cancel
	go func() {
		logger.Printf("WebSocket Gateway listening on %s%s", config.HTTP.Listen, config.HTTP.Path)
		errCh <- websocketmux.Serve(httpContext, httpListener, router)
	}()

	serveFinished := false
	var serveError error
	select {
	case err := <-errCh:
		serveFinished = true
		serveError = err
		stopSignals()
	case <-signalContext.Done():
	}
	logger.Printf("Gateway draining for up to %s", config.DrainTimeout.Duration)
	server.BeginDrain()
	httpHandler.BeginDrain()
	drainReportContext, cancelDrainReport := context.WithTimeout(context.Background(), 5*time.Second)
	if err := controlAgent.Drain(drainReportContext); err != nil {
		logger.Printf("report Data Plane drain failed: %v", err)
	}
	cancelDrainReport()
	drainContext, cancelDrain := context.WithTimeout(context.Background(), config.DrainTimeout.Duration)
	drainErr := server.Drain(drainContext)
	cancelDrain()
	if drainErr != nil {
		logger.Printf("Gateway drain deadline reached: %v", drainErr)
	}
	cancelHTTP()
	controlAgent.Stop()
	if !serveFinished {
		if err := <-errCh; err != nil {
			serveError = err
		}
	}
	if serveError != nil {
		errorLogger.Fatalf("Gateway listener stopped: %v", serveError)
	}
	logger.Print("Gateway stopped")
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"github.com/fengqi-dev/kube-loop/internal/logging"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/labstack/echo/v5"
)

var version = "dev"

func main() {
	drainTimeoutDefault, err := durationEnv("KUBELOOP_GATEWAY_DRAIN_TIMEOUT", 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	streamIdleTimeoutDefault, err := durationEnv("KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT", 30*time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	httpListenAddress := flag.String("http-listen", stringEnv("KUBELOOP_GATEWAY_HTTP_LISTEN", ":8080"), "WebSocket Gateway listen address")
	httpPath := flag.String("http-path", stringEnv("KUBELOOP_GATEWAY_HTTP_PATH", websocketmux.DefaultPath), "WebSocket Gateway path")
	verificationKeysFile := flag.String("relay-verification-keys-file", os.Getenv("KUBELOOP_RELAY_VERIFICATION_KEYS_FILE"), "RelayTicket public keys JSON file")
	ticketIssuer := flag.String("relay-ticket-issuer", os.Getenv("KUBELOOP_RELAY_TICKET_ISSUER"), "expected RelayTicket issuer")
	relayID := flag.String("relay-id", stringEnv("KUBELOOP_RELAY_ID", "primary"), "RelayTicket audience and Data Plane ID")
	relayControlPlaneURL := flag.String("relay-control-plane-url", os.Getenv("KUBELOOP_RELAY_CONTROL_PLANE_URL"), "Control Plane mTLS Relay Registry HTTPS origin")
	relayEndpoint := flag.String("relay-endpoint", os.Getenv("KUBELOOP_RELAY_ENDPOINT"), "externally routable WSS endpoint advertised to Control Plane")
	relayClientCertificateFile := flag.String("relay-client-cert-file", os.Getenv("KUBELOOP_RELAY_CLIENT_CERT_FILE"), "Relay Registry client certificate file")
	relayClientPrivateKeyFile := flag.String("relay-client-key-file", os.Getenv("KUBELOOP_RELAY_CLIENT_KEY_FILE"), "Relay Registry client private key file")
	relayServerCAFile := flag.String("relay-server-ca-file", os.Getenv("KUBELOOP_RELAY_SERVER_CA_FILE"), "Relay Registry server CA file")
	relayServerName := flag.String("relay-server-name", os.Getenv("KUBELOOP_RELAY_SERVER_NAME"), "Relay Registry TLS server name override")
	relayBearerTokenFile := flag.String("relay-bearer-token-file", os.Getenv("KUBELOOP_RELAY_BEARER_TOKEN_FILE"), "projected ServiceAccount token file for Relay Registry TokenReview")
	gatewayIP := flag.String("gateway-ip", os.Getenv("KUBELOOP_POD_IP"), "Gateway Pod IP used by traffic listeners")
	replayEntries := flag.Int("relay-replay-entries", relayticket.DefaultReplayEntries, "maximum live RelayTicket replay entries")
	maximumSessions := flag.Int("max-websocket-sessions", 256, "maximum physical WebSocket sessions")
	maximumSessionsPerUser := flag.Int("max-websocket-sessions-per-user", 8, "maximum physical WebSocket sessions per principal across devices")
	maximumStreamsPerSession := flag.Int("max-streams-per-session", 128, "maximum logical streams per WebSocket session")
	maximumFrameBytes := flag.Int64("max-websocket-frame-bytes", 1<<20, "maximum WebSocket binary frame bytes")
	minClientVersion := flag.String("min-client-version", os.Getenv("KUBELOOP_MIN_CLIENT_VERSION"), "minimum supported desktop client version")
	printResolvConf := flag.Bool("print-resolv-conf", false, "print the Pod DNS configuration and exit")
	drainTimeout := flag.Duration("drain-timeout", drainTimeoutDefault, "maximum time to drain active Gateway streams")
	streamIdleTimeout := flag.Duration("stream-idle-timeout", streamIdleTimeoutDefault, "maximum inactivity allowed for one logical stream")
	handshakeTimeout := flag.Duration("websocket-handshake-timeout", 10*time.Second, "maximum time to complete the WSS v2 handshake")
	logLevel := flag.String("log-level", stringEnv("KUBELOOP_LOG_LEVEL", "info"), "log level: debug, info, warn, or error")
	flag.Parse()
	if *printResolvConf {
		content, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stdout.Write(content); err != nil {
			log.Fatal(err)
		}
		return
	}

	parsedLogLevel, err := logging.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal(err)
	}
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsedLogLevel})
	componentLogger := slog.New(logHandler).With("component", "data-plane")
	logger := slog.NewLogLogger(componentLogger.Handler(), slog.LevelInfo)
	errorLogger := slog.NewLogLogger(componentLogger.Handler(), slog.LevelError)
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	server := gateway.NewServer(logger, 10*time.Second)
	errCh := make(chan error, 1)
	serveCount := 0
	var httpHandler *websocketmux.Handler
	var cancelHTTP context.CancelFunc
	var controlAgent *relayagent.Agent
	var runtimeReporter *relayRuntimeReporter
	var verifyRequest func(*http.Request) (relayticket.Claims, error)
	registryMode := strings.TrimSpace(*relayControlPlaneURL) != ""
	if registryMode || strings.TrimSpace(*verificationKeysFile) != "" {
		if !strings.HasPrefix(*httpPath, "/") {
			errorLogger.Fatal("WebSocket Gateway path must start with /")
		}
		if *maximumSessions < 1 || *maximumSessions > 1<<20 || *maximumStreamsPerSession < 1 ||
			*maximumStreamsPerSession > 1<<20 || *maximumSessionsPerUser < 1 ||
			*maximumSessionsPerUser > *maximumSessions || *maximumFrameBytes < 8<<10 || *maximumFrameBytes > 16<<20 ||
			uint64(*maximumSessions)*uint64(*maximumStreamsPerSession) > 1<<24 {
			errorLogger.Fatal("Gateway WebSocket capacity configuration is invalid")
		}
		var dynamicAuthenticator *relayagent.TicketAuthenticator
		if registryMode {
			var authenticatorErr error
			dynamicAuthenticator, authenticatorErr = relayagent.NewTicketAuthenticator(relayagent.TicketAuthenticatorConfig{
				Issuer: *ticketIssuer, RequiredOperation: "tunnel", ReplayEntries: *replayEntries,
			})
			if authenticatorErr != nil {
				errorLogger.Fatal(authenticatorErr)
			}
			verifyRequest = dynamicAuthenticator.Verify
		} else {
			verificationKeys, keyErr := relayticket.LoadVerificationKeys(*verificationKeysFile)
			if keyErr != nil {
				errorLogger.Fatal(keyErr)
			}
			verifier, verifierErr := relayticket.NewVerifier(relayticket.VerifierConfig{
				Keys: verificationKeys, Issuer: *ticketIssuer, Audience: *relayID,
				RequiredOperation: "tunnel",
			})
			if verifierErr != nil {
				errorLogger.Fatal(verifierErr)
			}
			replay, replayErr := relayticket.NewReplayGuard(*replayEntries, nil)
			if replayErr != nil {
				errorLogger.Fatal(replayErr)
			}
			requestVerifier, requestVerifierErr := relayticket.NewRequestVerifier(verifier, replay)
			if requestVerifierErr != nil {
				errorLogger.Fatal(requestVerifierErr)
			}
			verifyRequest = requestVerifier.Verify
		}
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
			Logger: logger, StreamIdleTimeout: *streamIdleTimeout, HandshakeTimeout: *handshakeTimeout,
			ServerVersion: version, MinClientVersion: strings.TrimSpace(*minClientVersion),
			MaxSessions: *maximumSessions, MaxSessionsPerUser: *maximumSessionsPerUser,
			MaxStreamsPerSession: *maximumStreamsPerSession, MaxFrameBytes: *maximumFrameBytes,
			Handle: func(identity websocketmux.Identity, connection net.Conn) {
				server.ServeConnForAuthorization(connection, gateway.SessionAuthorization{
					SessionID: identity.SessionID, Generation: identity.SessionGeneration,
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
		if registryMode {
			advertisedEndpoint, endpointErr := expandRelayEndpoint(*relayEndpoint)
			if endpointErr != nil {
				errorLogger.Fatal(endpointErr)
			}
			httpClient, clientErr := relayagent.NewHTTPClient(relayagent.ClientTLSConfig{
				CertificateFile: *relayClientCertificateFile, PrivateKeyFile: *relayClientPrivateKeyFile,
				ServerCAFile: *relayServerCAFile, ServerName: *relayServerName,
			})
			if clientErr != nil {
				errorLogger.Fatal(clientErr)
			}
			runtimeReporter = &relayRuntimeReporter{
				gateway: server, websocket: handler, maximumPhysical: uint32(*maximumSessions),
				maximumLogical: uint32(uint64(*maximumSessions) * uint64(*maximumStreamsPerSession)),
			}
			var agentErr error
			controlAgent, agentErr = relayagent.New(relayagent.Config{
				ControlPlaneURL: *relayControlPlaneURL, Endpoint: advertisedEndpoint, HTTPClient: httpClient,
				BearerTokenFile: *relayBearerTokenFile,
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
		}
		httpListener, listenErr := net.Listen("tcp", *httpListenAddress)
		if listenErr != nil {
			errorLogger.Fatal(listenErr)
		}
		router := echo.New()
		operations.NewHandler(operationsState, handler).Register(router)
		router.Any(*httpPath, echo.WrapHandler(handler))
		if registryMode {
			trafficHandler, trafficErr := trafficapi.New(trafficapi.Config{
				GatewayIP: strings.TrimSpace(*gatewayIP), VerifyRequest: verifyRequest, ControlPlane: controlAgent,
				MaximumSessions: *maximumSessions, OtherSessions: handler.ActiveSessions,
			})
			if trafficErr != nil {
				errorLogger.Fatal(trafficErr)
			}
			trafficHandler.RegisterRoutes(router)
			runtimeReporter.SetTraffic(trafficHandler)
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
			writer, request := ctx.Response(), ctx.Request()
			logger.Printf("WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=path", request.RemoteAddr, request.Method, request.URL.Path, http.StatusNotFound)
			http.NotFound(writer, request)
		}
		httpContext, cancel := context.WithCancel(context.Background())
		cancelHTTP = cancel
		serveCount++
		go func() {
			logger.Printf("WebSocket Gateway listening on %s%s", *httpListenAddress, *httpPath)
			errCh <- websocketmux.Serve(httpContext, httpListener, router)
		}()
	} else {
		logger.Print("WebSocket Gateway disabled: neither Relay Registry nor verification keys are configured")
	}
	if serveCount == 0 {
		errorLogger.Fatal("no Gateway listener is configured")
	}
	serveResults := 0
	var serveError error
	select {
	case err := <-errCh:
		serveResults++
		serveError = err
		stopSignals()
	case <-signalContext.Done():
	}
	logger.Printf("Gateway draining for up to %s", *drainTimeout)
	server.BeginDrain()
	if httpHandler != nil {
		httpHandler.BeginDrain()
	}
	if controlAgent != nil {
		drainReportContext, cancelDrainReport := context.WithTimeout(context.Background(), 5*time.Second)
		if err := controlAgent.Drain(drainReportContext); err != nil {
			logger.Printf("report Data Plane drain failed: %v", err)
		}
		cancelDrainReport()
	}
	drainContext, cancelDrain := context.WithTimeout(context.Background(), *drainTimeout)
	drainErr := server.Drain(drainContext)
	cancelDrain()
	if drainErr != nil {
		logger.Printf("Gateway drain deadline reached: %v", drainErr)
	}
	if cancelHTTP != nil {
		cancelHTTP()
	}
	if controlAgent != nil {
		controlAgent.Stop()
	}
	for serveResults < serveCount {
		if err := <-errCh; err != nil && serveError == nil {
			serveError = err
		}
		serveResults++
	}
	if serveError != nil {
		errorLogger.Fatalf("Gateway listener stopped: %v", serveError)
	}
	logger.Print("Gateway stopped")
}

func stringEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return duration, nil
}

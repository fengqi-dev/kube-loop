package websocketmux_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	clientmux "github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/correlation"
	servermux "github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func TestForwarderPropagatesCorrelationToTokenSourceAndGateway(t *testing.T) {
	const (
		correlationID = "44444444-4444-4444-8444-444444444444"
		deviceID      = "22222222-2222-4222-8222-222222222222"
		sessionID     = "33333333-3333-4333-8333-333333333333"
	)
	var clientLogs, gatewayLogs bytes.Buffer
	tokenCorrelation := make(chan string, 1)
	requestCorrelation := make(chan string, 1)
	handler, err := servermux.NewHandler(servermux.ServerConfig{
		Authenticator: servermux.AuthenticatorFunc(func(request *http.Request) (servermux.Identity, error) {
			requestCorrelation <- request.Header.Get(correlation.Header)
			return servermux.Identity{
				IdentityID: "identity", DeviceID: deviceID, SessionID: sessionID,
				SessionGeneration: 1, TicketID: "ticket-id", ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Logger: slog.New(slog.NewJSONHandler(&gatewayLogs, nil)),
		Handle: func(context.Context, servermux.Identity, net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := echo.New()
	router.Use(httpmiddleware.RequestID())
	router.Any(servermux.DefaultPath, echo.WrapHandler(handler))
	server := httptest.NewServer(router)
	defer server.Close()
	ctx := correlation.WithID(t.Context(), correlationID)
	forwarder, err := clientmux.Start(ctx, clientmux.ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http") + servermux.DefaultPath,
		TokenSource: func(ctx context.Context) (string, error) {
			tokenCorrelation <- correlation.ID(ctx)
			return "relay-ticket", nil
		},
		DeviceID: deviceID, SessionID: sessionID, SessionGeneration: 1,
		PoolSize: 1, Logger: slog.New(slog.NewJSONHandler(&clientLogs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := forwarder.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-tokenCorrelation; got != correlationID {
		t.Fatalf("token source correlation ID = %q", got)
	}
	if got := <-requestCorrelation; got != correlationID {
		t.Fatalf("Gateway header correlation ID = %q", got)
	}
	for name, output := range map[string]string{
		"client": clientLogs.String(), "gateway": gatewayLogs.String(),
	} {
		if !strings.Contains(output, `"correlation_id":"`+correlationID+`"`) ||
			strings.Contains(output, "relay-ticket") {
			t.Fatalf("%s logs = %q", name, output)
		}
	}
}

func TestForwarderSharesOnePhysicalWebSocketForTunnelAndTrafficStreams(t *testing.T) {
	const deviceID = "22222222-2222-4222-8222-222222222222"
	token, err := tunnel.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	var physicalConnections atomic.Int32
	results := make(chan error, 2)
	handler, err := servermux.NewHandler(servermux.ServerConfig{
		Authenticator: servermux.AuthenticatorFunc(func(request *http.Request) (servermux.Identity, error) {
			physicalConnections.Add(1)
			if request.Header.Get("Authorization") != "Bearer relay-ticket" {
				return servermux.Identity{}, errors.New("missing RelayTicket")
			}
			return servermux.Identity{
				IdentityID: "identity", DeviceID: deviceID, SessionID: uuid.NewString(),
				SessionGeneration: 1, ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Handle: func(ctx context.Context, _ servermux.Identity, connection net.Conn) {
			results <- handleTestStream(ctx, connection, token, taskID)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	forwarder, err := clientmux.Start(t.Context(), clientmux.ClientConfig{
		URL: "ws" + server.URL[len("http"):], Token: "relay-ticket", DeviceID: deviceID,
		PoolSize: 1, MaxPhysical: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	ordinary, err := net.Dial("tcp", forwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.WriteOpen(
		ordinary,
		tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: "10.42.0.5", Port: 8080},
		token,
	); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(ordinary); err != nil {
		t.Fatal(err)
	}
	if _, err := ordinary.Write([]byte("ordinary")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, len("ordinary-response"))
	if _, err := io.ReadFull(ordinary, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ordinary-response" {
		t.Fatalf("ordinary response = %q", reply)
	}
	_ = ordinary.Close()

	logical, err := forwarder.OpenStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.WriteTrafficOpen(
		logical,
		tunnel.TrafficOpenRequest{Mode: tunnel.TrafficModeExchange, TaskID: taskID},
		token,
	); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(logical); err != nil {
		t.Fatal(err)
	}
	framed, err := trafficstream.Dial(t.Context(), logical)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	if err != nil {
		t.Fatal(err)
	}
	if err := framed.WriteFrame(t.Context(), stop); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Gateway logical stream did not finish")
		}
	}
	if got := physicalConnections.Load(); got != 1 {
		t.Fatalf("physical /tunnel WebSocket connections = %d, want 1", got)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := forwarder.OpenStream(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled OpenStream error = %v", err)
	}
	if err := forwarder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := framed.WriteFrame(t.Context(), stop); err == nil {
		t.Fatal("tracked Traffic stream survived Forwarder.Close")
	}
}

func TestForwarderExpandsPoolAtStreamCapacity(t *testing.T) {
	const deviceID = "33333333-3333-4333-8333-333333333333"
	var physicalConnections atomic.Int32
	handler, err := servermux.NewHandler(servermux.ServerConfig{
		MaxSessions:          4,
		MaxSessionsPerUser:   4,
		MaxStreamsPerSession: 1,
		Authenticator: servermux.AuthenticatorFunc(func(*http.Request) (servermux.Identity, error) {
			physicalConnections.Add(1)
			return servermux.Identity{
				IdentityID: "identity", DeviceID: deviceID, SessionID: uuid.NewString(),
				SessionGeneration: 1, ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Handle: func(_ context.Context, _ servermux.Identity, connection net.Conn) {
			_, _ = io.Copy(io.Discard, connection)
			_ = connection.Close()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	forwarder, err := clientmux.Start(t.Context(), clientmux.ClientConfig{
		URL: "ws" + server.URL[len("http"):], Token: "relay-ticket", DeviceID: deviceID,
		PoolSize: 1, MaxPhysical: 2, MaxStreamsPerConn: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forwarder.Close() })
	first, err := forwarder.OpenStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := forwarder.OpenStream(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := physicalConnections.Load(); got != 2 {
		t.Fatalf("expanded physical connections = %d, want 2", got)
	}
	_ = first.Close()
	_ = second.Close()
}

func TestHandshakeErrorFormatting(t *testing.T) {
	var nilError *clientmux.HandshakeError
	if nilError.Error() != "" {
		t.Fatalf("nil handshake error = %q", nilError.Error())
	}
	withoutMessage := (&clientmux.HandshakeError{Code: "VERSION_MISMATCH"}).Error()
	if withoutMessage != "Gateway rejected WSS handshake: VERSION_MISMATCH" {
		t.Fatalf("handshake error = %q", withoutMessage)
	}
	withMessage := (&clientmux.HandshakeError{Code: "REJECTED", Message: "upgrade required"}).Error()
	if withMessage != "Gateway rejected WSS handshake: REJECTED: upgrade required" {
		t.Fatalf("handshake error = %q", withMessage)
	}
}

func handleTestStream(
	ctx context.Context,
	connection net.Conn,
	token tunnel.SessionToken,
	taskID string,
) (resultErr error) {
	defer func() {
		resultErr = errors.Join(resultErr, connection.Close())
	}()
	header, err := tunnel.ReadSessionHeader(connection)
	if err != nil {
		return err
	}
	if header.Token != token {
		return errors.New("logical stream Session token changed")
	}
	switch header.Command {
	case tunnel.CommandTCP:
		request, err := tunnel.ReadOpenBody(connection, header.Command)
		if err != nil {
			return err
		}
		if request.Address() != "10.42.0.5:8080" {
			return errors.New("ordinary tunnel target changed")
		}
		if err := tunnel.WriteStatus(connection, nil); err != nil {
			return err
		}
		payload := make([]byte, len("ordinary"))
		if _, err := io.ReadFull(connection, payload); err != nil {
			return err
		}
		_, err = connection.Write([]byte("ordinary-response"))
		return err
	case tunnel.CommandTraffic:
		request, err := tunnel.ReadTrafficOpenBody(connection)
		if err != nil {
			return err
		}
		if request.Mode != tunnel.TrafficModeExchange || request.TaskID != taskID {
			return errors.New("traffic Task selector changed")
		}
		if err := tunnel.WriteStatus(connection, nil); err != nil {
			return err
		}
		framed, err := trafficstream.Accept(ctx, connection)
		if err != nil {
			return err
		}
		encoded, err := framed.ReadFrame(ctx)
		if err != nil {
			return err
		}
		frame, err := exchangestream.Decode(encoded)
		if err != nil || frame.Type != exchangestream.Stop {
			return errors.New("traffic Task frame changed")
		}
		return nil
	default:
		return errors.New("unexpected tunnel command")
	}
}

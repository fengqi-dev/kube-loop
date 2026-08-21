package websocketmux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"

	shared "github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/protocol/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

const testDeviceID = "22222222-2222-4222-8222-222222222222"

func TestForwarderMultiplexesConcurrentStreams(t *testing.T) {
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("test-token"),
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			_, _ = io.Copy(connection, connection)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(handler.ServeHTTP))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	forwarder, err := Start(ctx, ClientConfig{
		URL:               "ws" + strings.TrimPrefix(server.URL, "http"),
		Token:             "test-token",
		DeviceID:          testDeviceID,
		PoolSize:          2,
		MaxStreamsPerConn: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = forwarder.Close() }()

	const streamCount = 24
	var wait sync.WaitGroup
	errorsCh := make(chan error, streamCount)
	for index := range streamCount {
		wait.Go(func() {
			connection, dialErr := net.DialTimeout("tcp", forwarder.Address(), time.Second)
			if dialErr != nil {
				errorsCh <- dialErr
				return
			}
			defer func() { _ = connection.Close() }()
			message := fmt.Sprintf("stream-%d\n", index)
			if _, writeErr := io.WriteString(connection, message); writeErr != nil {
				errorsCh <- writeErr
				return
			}
			response, readErr := bufio.NewReader(connection).ReadString('\n')
			if readErr != nil {
				errorsCh <- readErr
				return
			}
			if response != message {
				errorsCh <- fmt.Errorf("response %q, want %q", response, message)
			}
		})
	}
	wait.Wait()
	close(errorsCh)
	for testErr := range errorsCh {
		t.Error(testErr)
	}
}

func TestForwarderPreservesTCPHalfClose(t *testing.T) {
	result := make(chan error, 1)
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("test-token"),
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			request, readErr := io.ReadAll(connection)
			if readErr != nil {
				result <- readErr
				return
			}
			if string(request) != "request body" {
				result <- fmt.Errorf("request = %q", request)
				return
			}
			_, writeErr := io.WriteString(connection, "response after EOF")
			result <- writeErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	forwarder, err := Start(ctx, ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "test-token", DeviceID: testDeviceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = forwarder.Close() }()

	connection, err := net.Dial("tcp", forwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(connection, "request body"); err != nil {
		t.Fatal(err)
	}
	if err := connection.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(connection)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "response after EOF" {
		t.Fatalf("response = %q", response)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestSlowConsumerDoesNotBlockSiblingStream(t *testing.T) {
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSlow) }) }
	defer release()
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("test-token"),
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			var kind [1]byte
			if _, err := io.ReadFull(connection, kind[:]); err != nil {
				return
			}
			if kind[0] == 'S' {
				close(slowStarted)
				<-releaseSlow
				_, _ = io.Copy(io.Discard, connection)
				return
			}
			if kind[0] == 'F' {
				_, _ = io.WriteString(connection, "fast-response")
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	forwarder, err := Start(ctx, ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "test-token", DeviceID: testDeviceID,
		PoolSize: 1, MaxPhysical: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = forwarder.Close() }()

	slow, err := net.Dial("tcp", forwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Close() }()
	slowWrite := make(chan error, 1)
	go func() {
		if _, err := slow.Write(append([]byte{'S'}, bytes.Repeat([]byte{'x'}, 16<<20)...)); err != nil {
			slowWrite <- err
			return
		}
		slowWrite <- slow.(*net.TCPConn).CloseWrite()
	}()
	select {
	case <-slowStarted:
	case <-ctx.Done():
		t.Fatal("slow stream was not accepted")
	}
	fast, err := net.Dial("tcp", forwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fast.Close() }()
	_ = fast.SetDeadline(time.Now().Add(time.Second))
	started := time.Now()
	if _, err := fast.Write([]byte{'F'}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("fast-response"))
	if _, err := io.ReadFull(fast, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "fast-response" || time.Since(started) >= time.Second {
		t.Fatalf("sibling stream response = %q after %s", response, time.Since(started))
	}

	release()
	select {
	case err := <-slowWrite:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("slow stream did not resume after consumer release")
	}
}

func TestMalformedStreamDoesNotClosePhysicalSession(t *testing.T) {
	results := make(chan error, 4)
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("test-token"),
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			buffer := make([]byte, 32)
			for {
				read, err := connection.Read(buffer)
				if read > 0 {
					if _, writeErr := connection.Write(buffer[:read]); writeErr != nil {
						results <- writeErr
						return
					}
				}
				if err != nil {
					results <- err
					return
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, closeSession := rawClientSession(t, ctx, server.URL, "test-token")
	defer closeSession()

	malformed, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := malformed.Write([]byte{99, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := <-results; err == nil || !strings.Contains(err.Error(), "frame type") {
		t.Fatalf("malformed stream error = %v", err)
	}
	_ = malformed.Close()

	goodRaw, err := session.OpenStream()
	if err != nil {
		t.Fatalf("physical session closed with malformed sibling: %v", err)
	}
	good := protocolmux.NewStreamConn(goodRaw)
	defer func() { _ = good.Close() }()
	if _, err := good.Write([]byte("healthy")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("healthy"))
	if _, err := io.ReadFull(good, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "healthy" {
		t.Fatalf("healthy sibling response = %q", response)
	}
}

func TestIdleStreamTimesOutWithoutClosingPhysicalSession(t *testing.T) {
	results := make(chan error, 4)
	handler, err := NewHandler(ServerConfig{
		Authenticator:     testAuthenticator("test-token"),
		StreamIdleTimeout: 50 * time.Millisecond,
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			var request [1]byte
			if _, err := io.ReadFull(connection, request[:]); err != nil {
				results <- err
				return
			}
			_, err := connection.Write([]byte("ok"))
			results <- err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, closeSession := rawClientSession(t, ctx, server.URL, "test-token")
	defer closeSession()

	idle, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-results; err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("idle stream error = %v", err)
	}
	_ = idle.Close()

	goodRaw, err := session.OpenStream()
	if err != nil {
		t.Fatalf("physical session closed with idle sibling: %v", err)
	}
	good := protocolmux.NewStreamConn(goodRaw)
	defer func() { _ = good.Close() }()
	if _, err := good.Write([]byte{'G'}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(good, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "ok" {
		t.Fatalf("healthy sibling response = %q", response)
	}
	if err := <-results; err != nil {
		t.Fatal(err)
	}
}

//nolint:revive // Test helpers conventionally keep testing.T first for immediate failure reporting.
func rawClientSession(t *testing.T, ctx context.Context, serverURL, token string) (*smux.Session, func()) {
	t.Helper()
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http"), &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := wssprotocol.Write(ctx, connection, wssprotocol.NewClientHello("test", testDeviceID)); err != nil {
		_ = connection.CloseNow()
		t.Fatal(err)
	}
	message, err := wssprotocol.Read(ctx, connection)
	if err != nil || message.ServerHello == nil {
		_ = connection.CloseNow()
		t.Fatalf("WSS handshake = %#v, %v", message, err)
	}
	streamConnection := websocket.NetConn(ctx, connection, websocket.MessageBinary)
	session, err := smux.Client(streamConnection, smuxConfig())
	if err != nil {
		_ = connection.CloseNow()
		t.Fatal(err)
	}
	return session, func() {
		_ = session.Close()
		_ = connection.CloseNow()
	}
}

func TestForwarderRejectsInvalidToken(t *testing.T) {
	var logs bytes.Buffer
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator(
			"correct",
		),
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		Handle: func(context.Context, Identity, net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, err = Start(context.Background(), ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "wrong", DeviceID: testDeviceID,
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected HTTP 401, got %v", err)
	}
	if !strings.Contains(logs.String(), "reason=authentication") {
		t.Fatalf("missing authentication rejection log: %q", logs.String())
	}
	if strings.Contains(logs.String(), "correct") || strings.Contains(logs.String(), "wrong") {
		t.Fatalf("authentication log leaked a token: %q", logs.String())
	}
}

func TestHandlerLogsRequestID(t *testing.T) {
	var logs bytes.Buffer
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("correct"),
		Logger:        slog.New(slog.NewJSONHandler(&logs, nil)),
		Handle:        func(context.Context, Identity, net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, DefaultPath, nil)
	request.Header.Set("X-Request-ID", "33333333-3333-4333-8333-333333333333")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !strings.Contains(logs.String(), `"request_id":"33333333-3333-4333-8333-333333333333"`) {
		t.Fatalf("request ID missing from Gateway log: %q", logs.String())
	}
}

func TestHandlerRejectsNewSessionsWhileDraining(t *testing.T) {
	handler, err := NewHandler(
		ServerConfig{Authenticator: testAuthenticator("token"), Handle: func(context.Context, Identity, net.Conn) {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.BeginDrain()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/tunnel", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("Retry-After") != "5" {
		t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
	}
	if !handler.Draining() {
		t.Fatal("handler did not report draining")
	}
}

func TestNewGenerationLetsExistingStreamFinishAndRejectsOlderNewStreams(t *testing.T) {
	const sessionID = "33333333-3333-4333-8333-333333333333"
	started := make(chan struct{})
	release := make(chan struct{})
	handler, err := NewHandler(ServerConfig{
		Authenticator: AuthenticatorFunc(func(request *http.Request) (Identity, error) {
			switch request.Header.Get("Authorization") {
			case "Bearer generation-1":
				return Identity{
					IdentityID:        "identity",
					DeviceID:          testDeviceID,
					SessionID:         sessionID,
					SessionGeneration: 1,
					ExpiresAt:         testTicketExpiry(),
				}, nil
			case "Bearer generation-2":
				return Identity{
					IdentityID:        "identity",
					DeviceID:          testDeviceID,
					SessionID:         sessionID,
					SessionGeneration: 2,
					ExpiresAt:         testTicketExpiry(),
				}, nil
			default:
				return Identity{}, fmt.Errorf("authentication failed")
			}
		}),
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			var request [1]byte
			if _, readErr := io.ReadFull(connection, request[:]); readErr != nil {
				return
			}
			if request[0] == 'H' {
				close(started)
				<-release
			}
			_, _ = connection.Write(request[:])
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	oldSession, closeOld := rawClientSession(t, ctx, server.URL, "generation-1")
	defer closeOld()
	oldRaw, err := oldSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	oldStream := protocolmux.NewStreamConn(oldRaw)
	defer func() { _ = oldStream.Close() }()
	if _, err := oldStream.Write([]byte{'H'}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("older generation stream did not start")
	}

	newSession, closeNew := rawClientSession(t, ctx, server.URL, "generation-2")
	defer closeNew()
	newRaw, err := newSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	newStream := protocolmux.NewStreamConn(newRaw)
	defer func() { _ = newStream.Close() }()
	if _, err := newStream.Write([]byte{'N'}); err != nil {
		t.Fatal(err)
	}
	var response [1]byte
	if _, err := io.ReadFull(newStream, response[:]); err != nil || response[0] != 'N' {
		t.Fatalf("new generation stream response = %q, %v", response, err)
	}

	close(release)
	if _, err := io.ReadFull(oldStream, response[:]); err != nil || response[0] != 'H' {
		t.Fatalf("existing older generation stream was interrupted: %q, %v", response, err)
	}

	staleRaw, err := oldSession.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	stale := protocolmux.NewStreamConn(staleRaw)
	defer func() { _ = stale.Close() }()
	_ = stale.SetDeadline(time.Now().Add(time.Second))
	_, _ = stale.Write([]byte{'S'})
	if _, err := stale.Read(response[:]); err == nil {
		t.Fatal("older physical WebSocket opened a stream after a newer generation became active")
	}

	header := make(http.Header)
	header.Set("Authorization", "Bearer generation-1")
	connection, responseHTTP, err := websocket.Dial(
		ctx,
		"ws"+strings.TrimPrefix(server.URL, "http"),
		&websocket.DialOptions{
			HTTPHeader: header, Subprotocols: []string{Subprotocol},
		},
	)
	if connection != nil {
		_ = connection.CloseNow()
	}
	if err == nil || responseHTTP == nil || responseHTTP.StatusCode != http.StatusUnauthorized {
		t.Fatalf("older generation WebSocket handshake = response %#v, error %v", responseHTTP, err)
	}
}

func TestWSSHandshakeReturnsTypedVersionAndClientVersionRejections(t *testing.T) {
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("token"), MinClientVersion: "2.1.0",
		Handle: func(context.Context, Identity, net.Conn) { t.Error("rejected handshake opened a logical stream") },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")

	tests := []struct {
		name     string
		config   ClientConfig
		wantCode string
	}{
		{
			name: "protocol", wantCode: wssprotocol.CodeVersionMismatch,
			config: ClientConfig{URL: endpoint, Token: "token", DeviceID: testDeviceID, PoolSize: 1,
				SupportedVersions: []string{"9.0"}},
		},
		{
			name: "client version", wantCode: wssprotocol.CodeClientVersionUnsupported,
			config: ClientConfig{URL: endpoint, Token: "token", DeviceID: testDeviceID, PoolSize: 1,
				ClientVersion: "2.0.0"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder, startErr := Start(context.Background(), test.config)
			if forwarder != nil {
				_ = forwarder.Close()
				t.Fatal("rejected handshake returned a forwarder")
			}
			var handshakeErr *shared.HandshakeError
			if !errors.As(startErr, &handshakeErr) || handshakeErr.Code != test.wantCode {
				t.Fatalf("handshake error = %#v, %v", handshakeErr, startErr)
			}
			if test.wantCode == wssprotocol.CodeVersionMismatch &&
				!slices.Equal(handshakeErr.SupportedVersions, []string{wssprotocol.Version}) {
				t.Fatalf("supported versions = %#v", handshakeErr.SupportedVersions)
			}
		})
	}
	waitForActiveSessions(t, handler, 0)
}

func TestWSSHandshakeBindsDeviceAndLimitsConnectionsPerIdentity(t *testing.T) {
	const secondDeviceID = "44444444-4444-4444-8444-444444444444"
	handler, err := NewHandler(ServerConfig{
		Authenticator: AuthenticatorFunc(func(request *http.Request) (Identity, error) {
			switch request.Header.Get("Authorization") {
			case "Bearer first":
				return Identity{
					IdentityID:        "identity",
					DeviceID:          testDeviceID,
					SessionID:         "33333333-3333-4333-8333-333333333333",
					SessionGeneration: 1,
					ExpiresAt:         testTicketExpiry(),
				}, nil
			case "Bearer second":
				return Identity{
					IdentityID:        "identity",
					DeviceID:          secondDeviceID,
					SessionID:         "55555555-5555-4555-8555-555555555555",
					SessionGeneration: 1,
					ExpiresAt:         testTicketExpiry(),
				}, nil
			default:
				return Identity{}, errors.New("authentication failed")
			}
		}),
		MaxSessions: 4, MaxSessionsPerUser: 1, MaxStreamsPerSession: 7,
		MaxFrameBytes: 512 << 10, StreamIdleTimeout: 2 * time.Minute,
		Handle: func(context.Context, Identity, net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")

	wrongDevice, err := Start(context.Background(), ClientConfig{
		URL: endpoint, Token: "first", DeviceID: secondDeviceID, PoolSize: 1,
	})
	if wrongDevice != nil {
		_ = wrongDevice.Close()
	}
	var deviceErr *shared.HandshakeError
	if !errors.As(err, &deviceErr) || deviceErr.Code != wssprotocol.CodeDeviceMismatch {
		t.Fatalf("device mismatch = %#v, %v", deviceErr, err)
	}
	waitForActiveSessions(t, handler, 0)

	first, err := Start(context.Background(), ClientConfig{
		URL: endpoint, Token: "first", DeviceID: testDeviceID, PoolSize: 2, MaxPhysical: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if handler.ActiveSessions() != 1 {
		t.Fatalf("active physical sessions = %d, want negotiated per-user limit 1", handler.ActiveSessions())
	}

	second, err := Start(context.Background(), ClientConfig{
		URL: endpoint, Token: "second", DeviceID: secondDeviceID, PoolSize: 1,
	})
	if second != nil {
		_ = second.Close()
	}
	var capacityErr *shared.HandshakeError
	if !errors.As(err, &capacityErr) || capacityErr.Code != wssprotocol.CodeUserCapacityExceeded {
		t.Fatalf("per-user rejection = %#v, %v", capacityErr, err)
	}
}

func TestWSSServerHelloPublishesExactLimitsBeforeSmux(t *testing.T) {
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("token"), MaxSessions: 11, MaxSessionsPerUser: 3,
		MaxStreamsPerSession: 17, MaxFrameBytes: 768 << 10, StreamIdleTimeout: 90 * time.Second,
		ServerVersion: "2.5.0", Handle: func(context.Context, Identity, net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection := dialRawWebSocket(t, ctx, server.URL, "token")
	defer func() { _ = connection.CloseNow() }()
	if err := wssprotocol.Write(ctx, connection, wssprotocol.NewClientHello("2.5.0", testDeviceID)); err != nil {
		t.Fatal(err)
	}
	message, err := wssprotocol.Read(ctx, connection)
	if err != nil || message.ServerHello == nil {
		t.Fatalf("ServerHello = %#v, %v", message, err)
	}
	hello := message.ServerHello
	if hello.ProtocolVersion != wssprotocol.Version || hello.ServerVersion != "2.5.0" ||
		hello.Limits.MaximumFrameBytes != 768<<10 ||
		hello.Limits.MaximumStreamFrameBytes != protocolmux.MaximumStreamFrameBytes ||
		hello.Limits.MaximumStreamsPerConnection != 17 || hello.Limits.MaximumPhysicalConnections != 11 ||
		hello.Limits.MaximumConnectionsPerUser != 3 || hello.Limits.StreamIdleTimeoutMillis != 90000 {
		t.Fatalf("ServerHello limits = %#v", hello)
	}
}

//nolint:revive // Test helpers conventionally keep testing.T first for immediate failure reporting.
func dialRawWebSocket(t *testing.T, ctx context.Context, serverURL, token string) *websocket.Conn {
	t.Helper()
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http"), &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func waitForActiveSessions(t *testing.T, handler *Handler, expected int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for handler.ActiveSessions() != expected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if handler.ActiveSessions() != expected {
		t.Fatalf("active sessions = %d, want %d", handler.ActiveSessions(), expected)
	}
}

func testAuthenticator(expected string) AuthenticatorFunc {
	return func(request *http.Request) (Identity, error) {
		if request.Header.Get("Authorization") != "Bearer "+expected {
			return Identity{}, fmt.Errorf("authentication failed")
		}
		return Identity{
			IdentityID:        "identity",
			DeviceID:          testDeviceID,
			SessionID:         "33333333-3333-4333-8333-333333333333",
			SessionGeneration: 1,
			ExpiresAt:         testTicketExpiry(),
		}, nil
	}
}

func testTicketExpiry() time.Time {
	return time.Now().Add(time.Hour)
}

func TestHandlerRejectsExpiredRelayTicketIdentity(t *testing.T) {
	handler, err := NewHandler(ServerConfig{
		Authenticator: AuthenticatorFunc(func(*http.Request) (Identity, error) {
			return Identity{
				IdentityID: "identity", DeviceID: testDeviceID,
				SessionID: "33333333-3333-4333-8333-333333333333", SessionGeneration: 1,
				ExpiresAt: time.Now().Add(-time.Second),
			}, nil
		}),
		Handle: func(context.Context, Identity, net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/tunnel", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestEstablishedStreamSurvivesRelayTicketExpiry(t *testing.T) {
	expiresAt := time.Now().Add(500 * time.Millisecond)
	started := make(chan struct{})
	release := make(chan struct{})
	handler, err := NewHandler(ServerConfig{
		Authenticator: AuthenticatorFunc(func(*http.Request) (Identity, error) {
			return Identity{
				IdentityID: "identity", DeviceID: testDeviceID,
				SessionID: "33333333-3333-4333-8333-333333333333", SessionGeneration: 1,
				ExpiresAt: expiresAt,
			}, nil
		}),
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			var request [1]byte
			if _, readErr := io.ReadFull(connection, request[:]); readErr != nil {
				return
			}
			close(started)
			<-release
			_, _ = connection.Write(request[:])
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, closeSession := rawClientSession(t, ctx, server.URL, "token")
	defer closeSession()
	rawStream, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	stream := protocolmux.NewStreamConn(rawStream)
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte{'E'}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("Gateway stream did not start")
	}
	if delay := time.Until(expiresAt) + 100*time.Millisecond; delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("RelayTicket expiry wait timed out")
		}
	}
	close(release)
	var response [1]byte
	if _, err := io.ReadFull(stream, response[:]); err != nil || response[0] != 'E' {
		t.Fatalf("stream response after RelayTicket expiry = %q, %v", response, err)
	}
}

func TestForwarderValidatesEndpointAndMultiplexingLimits(t *testing.T) {
	for _, config := range []ClientConfig{
		{URL: "https://gateway.example.com", Token: "token"},
		{URL: "wss://gateway.example.com", Token: "token", PoolSize: 9},
		{URL: "wss://gateway.example.com", Token: "token", MaxPhysical: 17},
		{URL: "wss://gateway.example.com", Token: "token", MaxStreamsPerConn: 1025},
	} {
		if _, err := Start(context.Background(), config); err == nil {
			t.Fatalf("expected validation error for %+v", config)
		}
	}
}

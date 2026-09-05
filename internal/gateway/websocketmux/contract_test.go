package websocketmux

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"

	shared "github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wss"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/transport/websocketmux"
)

func TestContractNewClientAndOldGatewayClassifiesVersionMismatch(t *testing.T) {
	oldGateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).
			Upgrade(writer, request, nil)

		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		streamConnection := protocolmux.NewWebSocketConn(request.Context(), connection, websocket.BinaryMessage)
		session, err := smux.Server(streamConnection, protocolmux.SmuxConfig())
		if err != nil {
			return
		}
		defer func() { _ = session.Close() }()
		_, _ = session.AcceptStream()
	}))
	defer oldGateway.Close()

	forwarder, err := Start(context.Background(), ClientConfig{
		URL: "ws" + strings.TrimPrefix(oldGateway.URL, "http"), Token: "token",
		DeviceID: testDeviceID, PoolSize: 1, HandshakeTimeout: 500 * time.Millisecond,
	})
	if forwarder != nil {
		_ = forwarder.Close()
		t.Fatal("new client created a partial Forwarder against an old Gateway")
	}
	var handshakeErr *shared.HandshakeError
	if !errors.As(err, &handshakeErr) || handshakeErr.Code != wss.CodeVersionMismatch {
		t.Fatalf("old-Gateway error = %#v, %v", handshakeErr, err)
	}
}

func TestContractNewClientRejectsUnknownAndMissingServerHelloFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown field",
			raw:  `{"type":"server_hello","protocolVersion":"2.0","serverVersion":"2.0.0","capabilities":["smux.v2","tunnel.control.v2"],"limits":{"maximumFrameBytes":1048576,"maximumStreamFrameBytes":65536,"maximumStreamsPerConnection":128,"maximumPhysicalConnections":256,"maximumConnectionsPerUser":8,"streamIdleTimeoutMs":1800000},"future":true}`,
		},
		{
			name: "missing limits",
			raw:  `{"type":"server_hello","protocolVersion":"2.0","serverVersion":"2.0.0","capabilities":["smux.v2","tunnel.control.v2"]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := malformedHandshakeGateway(t, []byte(test.raw))
			defer gateway.Close()
			forwarder, err := Start(context.Background(), ClientConfig{
				URL: "ws" + strings.TrimPrefix(gateway.URL, "http"), Token: "token",
				DeviceID: testDeviceID, PoolSize: 1,
			})
			if forwarder != nil {
				_ = forwarder.Close()
				t.Fatal("malformed ServerHello created a partial Forwarder")
			}
			var handshakeErr *shared.HandshakeError
			if !errors.As(err, &handshakeErr) || handshakeErr.Code != wss.CodeInvalidHandshake {
				t.Fatalf("malformed ServerHello error = %#v, %v", handshakeErr, err)
			}
		})
	}
}

func TestContractNewGatewayRejectsUnknownAndMissingClientHelloFields(t *testing.T) {
	handler, err := NewHandler(ServerConfig{
		Authenticator: testAuthenticator("token"),
		Handle: func(context.Context, Identity, net.Conn) {
			t.Error("malformed ClientHello opened a partial logical session")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "unknown field",
			raw:  `{"type":"client_hello","protocolVersions":["2.0"],"clientVersion":"2.0.0","deviceId":"22222222-2222-4222-8222-222222222222","capabilities":["smux.v2","tunnel.control.v2"],"future":true}`,
		},
		{
			name: "missing device",
			raw:  `{"type":"client_hello","protocolVersions":["2.0"],"clientVersion":"2.0.0","capabilities":["smux.v2","tunnel.control.v2"]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			connection := dialRawWebSocket(t, ctx, server.URL, "token")
			if err := connection.WriteMessage(websocket.BinaryMessage, []byte(test.raw)); err != nil {
				_ = connection.Close()
				t.Fatal(err)
			}
			message, err := wss.Read(ctx, connection)
			_ = connection.Close()
			if err != nil || message.Reject == nil || message.Reject.Code != wss.CodeInvalidHandshake {
				t.Fatalf("malformed ClientHello rejection = %#v, %v", message, err)
			}
		})
	}
	waitForNoActiveSessions(t, handler)
}

func malformedHandshakeGateway(t *testing.T, response []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(wss.VersionHeader, wss.Version)
		connection, err := (&websocket.Upgrader{Subprotocols: []string{Subprotocol}}).
			Upgrade(writer, request, http.Header{
				wss.VersionHeader: []string{wss.Version},
			})

		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		message, err := wss.Read(ctx, connection)
		if err != nil || message.ClientHello == nil {
			return
		}
		_ = connection.WriteMessage(websocket.BinaryMessage, response)
	}))
}

package wss

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReadWriteRoundTripOverWebSocket(t *testing.T) {
	serverResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			serverResult <- err
			return
		}
		defer func() { _ = connection.Close() }()
		message, err := Read(request.Context(), connection)
		if err != nil {
			serverResult <- err
			return
		}
		if message.ClientHello == nil || message.ClientHello.DeviceID != "device-1" {
			serverResult <- fmt.Errorf("client hello = %#v", message.ClientHello)
			return
		}
		serverResult <- Write(request.Context(), connection, NewReject(CodeVersionMismatch, "upgrade required", Version))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if err := Write(ctx, connection, NewClientHello("2.4.0", "device-1")); err != nil {
		t.Fatal(err)
	}
	message, err := Read(ctx, connection)
	if err != nil || message.Reject == nil || message.Reject.Code != CodeVersionMismatch {
		t.Fatalf("server reject = %#v, error = %v", message.Reject, err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestReadRejectsTextWebSocketMessage(t *testing.T) {
	serverResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err == nil {
			defer func() { _ = connection.Close() }()
			err = connection.WriteMessage(websocket.TextMessage, []byte(`{"type":"reject"}`))
		}
		serverResult <- err
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if _, err := Read(ctx, connection); !errors.Is(err, ErrInvalidHandshake) {
		t.Fatalf("Read() error = %v, want ErrInvalidHandshake", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

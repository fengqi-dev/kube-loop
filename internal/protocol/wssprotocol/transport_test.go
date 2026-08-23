package wssprotocol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

func TestReadWriteRoundTripOverWebSocket(t *testing.T) {
	serverResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			serverResult <- err
			return
		}
		defer func() { _ = connection.CloseNow() }()
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
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
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
		connection, err := websocket.Accept(writer, request, nil)
		if err == nil {
			defer func() { _ = connection.CloseNow() }()
			err = connection.Write(request.Context(), websocket.MessageText, []byte(`{"type":"reject"}`))
		}
		serverResult <- err
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	if _, err := Read(ctx, connection); !errors.Is(err, ErrInvalidHandshake) {
		t.Fatalf("Read() error = %v, want ErrInvalidHandshake", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

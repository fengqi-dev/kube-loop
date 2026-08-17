package websocket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDialAcceptRoundTrip(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Test-Header") != "present" {
			t.Error("dial header was not forwarded")
		}
		writer.Header().Set("X-Upgrade-Contract", "retained")
		connection, err := Accept(writer, request, &AcceptOptions{Subprotocols: []string{"test.v1"}})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer connection.CloseNow()
		messageType, payload, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		if err := connection.Write(request.Context(), messageType, payload); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, response, err := Dial(ctx, "wss"+strings.TrimPrefix(server.URL, "https"), &DialOptions{
		HTTPClient:   server.Client(),
		HTTPHeader:   http.Header{"X-Test-Header": {"present"}},
		Subprotocols: []string{"test.v1"},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer connection.CloseNow()
	if response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected upgrade response: %#v", response)
	}
	if response.TLS == nil {
		t.Fatal("TLS upgrade response state is missing")
	}
	if response.Header.Get("X-Upgrade-Contract") != "retained" {
		t.Fatalf("upgrade response header = %q, want retained", response.Header.Get("X-Upgrade-Contract"))
	}
	if connection.Subprotocol() != "test.v1" {
		t.Fatalf("subprotocol = %q, want test.v1", connection.Subprotocol())
	}
	if err := connection.Write(ctx, MessageBinary, []byte("payload")); err != nil {
		t.Fatalf("write: %v", err)
	}
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if messageType != MessageBinary || string(payload) != "payload" {
		t.Fatalf("message = (%d, %q), want binary payload", messageType, payload)
	}
}

func TestReadCancellationClosesConnection(t *testing.T) {
	t.Parallel()
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer connection.CloseNow()
		close(accepted)
		_, _, _ = connection.Read(request.Context())
	}))
	defer server.Close()

	connection, _, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer connection.CloseNow()
	<-accepted
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, readErr := connection.Read(ctx)
		result <- readErr
	}()
	cancel()
	select {
	case readErr := <-result:
		if !errors.Is(readErr, context.Canceled) {
			t.Fatalf("read error = %v, want context canceled", readErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not stop after cancellation")
	}
}

func TestNetConnRoundTrip(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		stream := NetConn(request.Context(), connection, MessageBinary)
		defer stream.Close()
		payload := make([]byte, 5)
		if _, err := io.ReadFull(stream, payload); err != nil {
			t.Errorf("read stream: %v", err)
			return
		}
		if string(payload) != "hello" {
			t.Errorf("payload = %q, want hello", payload)
			return
		}
		if _, err := stream.Write([]byte("world")); err != nil {
			t.Errorf("write stream: %v", err)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	stream := NetConn(ctx, connection, MessageBinary)
	defer stream.Close()
	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("write stream: %v", err)
	}
	payload := make([]byte, 5)
	if _, err := io.ReadFull(stream, payload); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if string(payload) != "world" {
		t.Fatalf("payload = %q, want world", payload)
	}
}

func TestNetConnCancellation(t *testing.T) {
	t.Parallel()
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer connection.CloseNow()
		close(accepted)
		_, _, _ = connection.Read(request.Context())
	}))
	defer server.Close()

	connection, _, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := NetConn(ctx, connection, MessageBinary)
	defer stream.Close()
	<-accepted
	result := make(chan error, 1)
	go func() {
		_, readErr := stream.Read(make([]byte, 1))
		result <- readErr
	}()
	cancel()
	select {
	case readErr := <-result:
		if !errors.Is(readErr, context.Canceled) {
			t.Fatalf("stream read error = %v, want context canceled", readErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream read did not stop after cancellation")
	}
}

func TestPingRejectsCanceledContext(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(request.Context())
	}))
	defer server.Close()

	connection, _, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer connection.CloseNow()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := connection.Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ping error = %v, want context canceled", err)
	}
}

func TestCloseStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := Accept(writer, request, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		_ = connection.Close(StatusPolicyViolation, "denied")
	}))
	defer server.Close()

	connection, _, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer connection.CloseNow()
	_, _, err = connection.Read(context.Background())
	if code := CloseStatus(err); code != StatusPolicyViolation {
		t.Fatalf("close status = %d, want %d (error: %v)", code, StatusPolicyViolation, err)
	}
}

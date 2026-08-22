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
		defer func() { _ = connection.CloseNow() }()
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
	defer func() { _ = connection.CloseNow() }()
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
	if err := connection.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
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
		defer func() { _ = connection.CloseNow() }()
		close(accepted)
		_, _, _ = connection.Read(request.Context())
	}))
	defer server.Close()

	connection, _, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = connection.CloseNow() }()
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
		defer func() { _ = stream.Close() }()
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
	defer func() { _ = stream.Close() }()
	if stream.LocalAddr() == nil || stream.RemoteAddr() == nil {
		t.Fatalf("stream addresses = %v / %v", stream.LocalAddr(), stream.RemoteAddr())
	}
	deadline := time.Now().Add(5 * time.Second)
	if err := stream.SetDeadline(deadline); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if err := stream.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if err := stream.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("set write deadline: %v", err)
	}
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

func TestOriginCheckerAppliesSameHostAndPatternPolicy(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		origin   string
		patterns []string
		insecure bool
		expected bool
	}{
		{name: "missing origin", host: "gateway.example", expected: true},
		{name: "same host", host: "gateway.example:443", origin: "https://gateway.example", expected: true},
		{
			name: "host pattern", host: "gateway.example", origin: "https://app.example.test",
			patterns: []string{"*.example.test"}, expected: true,
		},
		{
			name: "URL pattern", host: "gateway.example", origin: "https://app.example.test",
			patterns: []string{"https://*.example.test"}, expected: true,
		},
		{name: "foreign origin", host: "gateway.example", origin: "https://attacker.example", expected: false},
		{name: "malformed origin", host: "gateway.example", origin: "://bad", expected: false},
		{
			name: "insecure override", host: "gateway.example", origin: "https://attacker.example",
			insecure: true, expected: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://"+test.host+"/socket", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			checker := originChecker(&AcceptOptions{
				OriginPatterns:     test.patterns,
				InsecureSkipVerify: test.insecure,
			})
			if got := checker(request); got != test.expected {
				t.Fatalf("origin allowed = %t, want %t", got, test.expected)
			}
		})
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
		defer func() { _ = connection.CloseNow() }()
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
	defer func() { _ = stream.Close() }()
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
		defer func() { _ = connection.CloseNow() }()
		_, _, _ = connection.Read(request.Context())
	}))
	defer server.Close()

	connection, _, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = connection.CloseNow() }()
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
	defer func() { _ = connection.CloseNow() }()
	_, _, err = connection.Read(context.Background())
	if code := CloseStatus(err); code != StatusPolicyViolation {
		t.Fatalf("close status = %d, want %d (error: %v)", code, StatusPolicyViolation, err)
	}
}

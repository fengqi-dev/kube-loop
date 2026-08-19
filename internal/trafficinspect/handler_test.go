package trafficinspect

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpc_testing "google.golang.org/grpc/interop/grpc_testing"
)

func TestHandler_HTTPProtocolsUseInjectedDialer(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create persisted authority: %v", err)
	}
	ca := authority.TLSCertificate()
	routes := newRoutingDialer()
	requestEvents := make(chan requestEvent, 8)
	output, err := NewChannelSink(8)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, ca, routes, requestEvents, output)

	plainOrigin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Upstream-Protocol", request.Proto)
		_, err := response.Write([]byte("plain"))
		if err != nil {
			t.Errorf("write plain response: %v", err)
		}
	}))
	t.Cleanup(plainOrigin.Close)
	routes.add("http.test:80", plainOrigin.Listener.Addr().String())

	tlsOrigin := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Upstream-Protocol", request.Proto)
		_, err := response.Write([]byte("tls"))
		if err != nil {
			t.Errorf("write TLS response: %v", err)
		}
	}))
	tlsOrigin.EnableHTTP2 = true
	tlsOrigin.StartTLS()
	t.Cleanup(tlsOrigin.Close)
	routes.add("https.test:443", tlsOrigin.Listener.Addr().String())

	tests := []struct {
		name            string
		url             string
		target          string
		clientHTTP2     bool
		wantClientProto int
		wantBody        string
	}{
		{
			name:            "HTTP/1.1",
			url:             "http://http.test/poc",
			target:          "http.test:80",
			wantClientProto: 1,
			wantBody:        "plain",
		},
		{
			name:            "HTTPS over HTTP/1.1",
			url:             "https://https.test/poc",
			target:          "https.test:443",
			wantClientProto: 1,
			wantBody:        "tls",
		},
		{
			name:            "HTTPS over HTTP/2",
			url:             "https://https.test/poc",
			target:          "https.test:443",
			clientHTTP2:     true,
			wantClientProto: 2,
			wantBody:        "tls",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newHTTPClient(t, handler, ca, test.target, test.clientHTTP2)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, test.url, nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("perform request: %v", err)
			}
			body, err := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if closeErr != nil {
				t.Fatalf("close response: %v", closeErr)
			}
			if response.ProtoMajor != test.wantClientProto {
				t.Fatalf("client protocol = %s, want HTTP/%d", response.Proto, test.wantClientProto)
			}
			if string(body) != test.wantBody {
				t.Fatalf("body = %q, want %q", body, test.wantBody)
			}
		})
	}

	seen := make(map[string]requestEvent)
	for range len(tests) {
		select {
		case event := <-requestEvents:
			seen[event.host+event.protocol] = event
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for HTTP inspection event")
		}
	}
	if len(seen) != len(tests) {
		t.Fatalf("unique inspection events = %d, want %d: %#v", len(seen), len(tests), seen)
	}
	for _, target := range []string{"http.test:80", "https.test:443"} {
		if routes.count(target) == 0 {
			t.Errorf("injected dialer did not receive target %q", target)
		}
	}
	flows := make(map[string]map[EventType]bool)
	protocolCounts := make(map[Protocol]int)
	for range len(tests) * 2 {
		select {
		case event := <-output.Events():
			protocolCounts[event.Protocol]++
			if flows[event.FlowID] == nil {
				flows[event.FlowID] = make(map[EventType]bool)
			}
			flows[event.FlowID][event.Type] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for structured HTTP event")
		}
	}
	if protocolCounts[ProtocolHTTP] != 2 || protocolCounts[ProtocolHTTPS] != 4 {
		t.Fatalf("protocol counts = %#v", protocolCounts)
	}
	for flowID, types := range flows {
		if !types[EventTypeRequest] || !types[EventTypeResponse] {
			t.Errorf("flow %s event types = %#v", flowID, types)
		}
	}
}

func TestHandlerDynamicSwitchAppliesWithoutRecreatingHandler(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatal(err)
	}
	routes := newRoutingDialer()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("dynamic"))
	}))
	t.Cleanup(origin.Close)
	routes.add("dynamic.test:80", origin.Listener.Addr().String())
	requests := make(chan struct{}, 1)
	var enabled atomic.Bool
	handler, err := New(Config{
		CA: authority.TLSCertificate(), DialContext: routes.DialContext,
		Enabled: enabled.Load,
		OnRequest: func(*http.Request) {
			requests <- struct{}{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	request := func() {
		client := newHTTPClient(t, handler, authority.TLSCertificate(), "dynamic.test:80", false)
		response, requestErr := client.Get("http://dynamic.test/poc")
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}

	request()
	select {
	case <-requests:
		t.Fatal("disabled handler inspected request")
	case <-time.After(50 * time.Millisecond):
	}
	enabled.Store(true)
	request()
	select {
	case <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("enabled handler did not inspect request")
	}
}

func TestHandler_H2CUsesInjectedDialer(t *testing.T) {
	ca := newTestCertificate(t, "KubeLoop POC CA", true)
	routes := newRoutingDialer()
	requestEvents := make(chan requestEvent, 1)
	handler := newTestHandler(t, ca, routes, requestEvents)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Upstream-Protocol", request.Proto)
			if _, err := response.Write([]byte("h2c")); err != nil {
				t.Errorf("write h2c response: %v", err)
			}
		},
	))
	origin.Config.Protocols = new(http.Protocols)
	origin.Config.Protocols.SetUnencryptedHTTP2(true)
	origin.Start()
	t.Cleanup(origin.Close)
	routes.add("h2c.test:80", origin.Listener.Addr().String())

	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			return dialThroughInspector(t, ctx, handler, "h2c.test:80"), nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://h2c.test/poc", nil)
	if err != nil {
		t.Fatalf("create h2c request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform h2c request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil {
		t.Fatalf("read h2c response: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("close h2c response: %v", closeErr)
	}
	if response.ProtoMajor != 2 || response.Header.Get("X-Upstream-Protocol") != "HTTP/2.0" {
		t.Fatalf("h2c protocols: client=%s upstream=%s", response.Proto, response.Header.Get("X-Upstream-Protocol"))
	}
	if string(body) != "h2c" {
		t.Fatalf("h2c body = %q, want h2c", body)
	}
	if routes.count("h2c.test:80") == 0 {
		t.Fatal("h2c traffic did not use the injected dialer")
	}
	select {
	case event := <-requestEvents:
		if event.protocol != "HTTP/2.0" {
			t.Fatalf("inspected h2c protocol = %s, want HTTP/2.0", event.protocol)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for h2c inspection event")
	}
}

func TestHandler_UnrecognizedProtocolsAreRelayedWithoutInspection(t *testing.T) {
	ca := newTestCertificate(t, "KubeLoop POC CA", true)
	routes := newRoutingDialer()
	echoAddress := startTCPEchoServer(t)
	routes.add("unknown.test:30000", echoAddress)
	requestEvents := make(chan requestEvent, 1)
	output, err := NewChannelSink(1)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, ca, routes, requestEvents, output)

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "unknown plaintext", payload: []byte("kubeloop tcp echo\n")},
		{name: "Redis RESP", payload: []byte("*1\r\n$4\r\nPING\r\n")},
		{name: "MySQL packet", payload: []byte{0x01, 0x00, 0x00, 0x00, 0x0e}},
		{name: "arbitrary binary", payload: []byte{0x00, 0xff, 0x7f, 0x10}},
		{name: "invalid TLS-like prefix", payload: []byte{0x16, 0x02, 0x00, 0x00}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := dialThroughInspector(t, t.Context(), handler, "unknown.test:30000")
			defer connection.Close()
			if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := connection.Write(test.payload); err != nil {
				t.Fatalf("write unrecognized payload: %v", err)
			}
			echoed := make([]byte, len(test.payload))
			if _, err := io.ReadFull(connection, echoed); err != nil {
				t.Fatalf("read unrecognized payload echo: %v", err)
			}
			if !bytes.Equal(echoed, test.payload) {
				t.Fatalf("echoed payload = %x, want %x", echoed, test.payload)
			}
		})
	}
	if routes.count("unknown.test:30000") != len(tests) {
		t.Fatalf("uninspected relay dials = %d, want %d", routes.count("unknown.test:30000"), len(tests))
	}
	select {
	case event := <-requestEvents:
		t.Fatalf("unrecognized traffic entered HTTP inspector: %#v", event)
	default:
	}
	select {
	case event := <-output.Events():
		t.Fatalf("unrecognized traffic emitted an inspection event: %#v", event)
	default:
	}
}

func TestHandler_PinsAuthorityChangesToOriginalDestination(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		requestURL  string
		requestHost string
		blockedDial string
	}{
		{
			name:        "Kubernetes Service DNS",
			target:      "10.96.12.34:80",
			requestURL:  "http://my-service.default.svc/",
			requestHost: "my-service.default.svc",
			blockedDial: "my-service.default.svc:80",
		},
		{
			name:        "local Port Forward",
			target:      "gateway.internal:49000",
			requestURL:  "http://127.0.0.1:60413/",
			requestHost: "127.0.0.1:60413",
			blockedDial: "127.0.0.1:60413",
		},
		{
			name:        "untrusted Host change",
			target:      "allowed.test:80",
			requestURL:  "http://blocked.test/resource",
			requestHost: "blocked.test",
			blockedDial: "blocked.test:80",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ca := newTestCertificate(t, "KubeLoop POC CA", true)
			routes := newRoutingDialer()
			originRequests := make(chan *http.Request, 1)
			origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				originRequests <- request.Clone(request.Context())
				response.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(origin.Close)
			routes.add(test.target, origin.Listener.Addr().String())

			handler := newTestHandler(t, ca, routes, nil)
			client := newHTTPClient(t, handler, ca, test.target, false)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, test.requestURL, nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("perform request: %v", err)
			}
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Fatalf("close response body: %v", closeErr)
			}
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
			}
			if routes.count(test.target) == 0 {
				t.Fatalf("original destination %q was not dialed", test.target)
			}
			if routes.count(test.blockedDial) != 0 {
				t.Fatalf("request authority %q reached the injected dialer", test.blockedDial)
			}
			select {
			case originRequest := <-originRequests:
				if originRequest.Host != test.requestHost {
					t.Fatalf("upstream Host = %q, want %q", originRequest.Host, test.requestHost)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for upstream request")
			}
		})
	}
}

func TestHandler_PreservesTLSAuthorityWhileDialingOriginalDestination(t *testing.T) {
	ca := newTestCertificate(t, "KubeLoop POC CA", true)
	routes := newRoutingDialer()
	originRequests := make(chan *http.Request, 1)
	serverNames := make(chan string, 1)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		originRequests <- request.Clone(request.Context())
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(origin.Close)
	origin.TLS.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		serverNames <- hello.ServerName
		return nil, nil
	}
	routes.add("10.96.12.34:9001", origin.Listener.Addr().String())

	handler := newTestHandler(t, ca, routes, nil)
	client := newHTTPClient(t, handler, ca, "10.96.12.34:9001", false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://allowed.test/resource", nil)
	if err != nil {
		t.Fatalf("create nondefault-port request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform nondefault-port request: %v", err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatalf("close nondefault-port response: %v", closeErr)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	if routes.count("10.96.12.34:9001") == 0 {
		t.Fatal("request did not use the original destination")
	}
	select {
	case originRequest := <-originRequests:
		if originRequest.Host != "allowed.test" {
			t.Fatalf("upstream authority = %q, want allowed.test", originRequest.Host)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream request")
	}
	select {
	case serverName := <-serverNames:
		if serverName != "allowed.test" {
			t.Fatalf("upstream TLS ServerName = %q, want allowed.test", serverName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream TLS handshake")
	}
}

func TestHandler_GRPCStreamingModes(t *testing.T) {
	ca := newTestCertificate(t, "KubeLoop POC CA", true)
	serverCertificate := newTestCertificate(t, "grpc.test", false)
	routes := newRoutingDialer()
	requestEvents := make(chan requestEvent, 8)
	output, err := NewChannelSink(16)
	if err != nil {
		t.Fatal(err)
	}
	handler := newTestHandler(t, ca, routes, requestEvents, output)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for gRPC server: %v", err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{*serverCertificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"},
	})))
	grpc_testing.RegisterTestServiceServer(server, grpcPOCServer{})
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil {
			if !errors.Is(serveErr, grpc.ErrServerStopped) {
				t.Errorf("serve gRPC: %v", serveErr)
			}
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	routes.add("grpc.test:443", listener.Addr().String())

	pool := x509.NewCertPool()
	pool.AddCert(ca.Leaf)
	connection, err := grpc.NewClient(
		"passthrough:///grpc.test:443",
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    pool,
			ServerName: "grpc.test",
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2"},
		})),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return dialThroughInspector(t, t.Context(), handler, "grpc.test:443"), nil
		}),
	)
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := connection.Close(); closeErr != nil {
			t.Errorf("close gRPC client: %v", closeErr)
		}
	})
	client := grpc_testing.NewTestServiceClient(connection)

	t.Run("unary", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		response, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{
			Payload: &grpc_testing.Payload{Body: []byte("unary")},
		})
		if err != nil {
			t.Fatalf("unary call: %v", err)
		}
		if string(response.GetPayload().GetBody()) != "unary" {
			t.Fatalf("unary payload = %q", response.GetPayload().GetBody())
		}
	})

	t.Run("server streaming", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		stream, err := client.StreamingOutputCall(ctx, &grpc_testing.StreamingOutputCallRequest{
			ResponseParameters: []*grpc_testing.ResponseParameters{{Size: 3}, {Size: 5}},
		})
		if err != nil {
			t.Fatalf("start server stream: %v", err)
		}
		for _, size := range []int{3, 5} {
			response, recvErr := stream.Recv()
			if recvErr != nil {
				t.Fatalf("receive server stream: %v", recvErr)
			}
			if len(response.GetPayload().GetBody()) != size {
				t.Fatalf("server stream payload size = %d, want %d", len(response.GetPayload().GetBody()), size)
			}
		}
		if _, recvErr := stream.Recv(); recvErr != io.EOF {
			t.Fatalf("server stream terminal error = %v, want EOF", recvErr)
		}
	})

	t.Run("client streaming", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		stream, err := client.StreamingInputCall(ctx)
		if err != nil {
			t.Fatalf("start client stream: %v", err)
		}
		for _, payload := range []string{"one", "three"} {
			if sendErr := stream.Send(&grpc_testing.StreamingInputCallRequest{
				Payload: &grpc_testing.Payload{Body: []byte(payload)},
			}); sendErr != nil {
				t.Fatalf("send client stream: %v", sendErr)
			}
		}
		response, err := stream.CloseAndRecv()
		if err != nil {
			t.Fatalf("close client stream: %v", err)
		}
		if response.GetAggregatedPayloadSize() != 8 {
			t.Fatalf("aggregated size = %d, want 8", response.GetAggregatedPayloadSize())
		}
	})

	t.Run("bidirectional streaming", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		stream, err := client.FullDuplexCall(ctx)
		if err != nil {
			t.Fatalf("start bidirectional stream: %v", err)
		}
		for _, size := range []int32{4, 7} {
			if sendErr := stream.Send(&grpc_testing.StreamingOutputCallRequest{
				ResponseParameters: []*grpc_testing.ResponseParameters{{Size: size}},
			}); sendErr != nil {
				t.Fatalf("send bidirectional request: %v", sendErr)
			}
			response, recvErr := stream.Recv()
			if recvErr != nil {
				t.Fatalf("receive bidirectional response: %v", recvErr)
			}
			if len(response.GetPayload().GetBody()) != int(size) {
				t.Fatalf("bidirectional payload size = %d, want %d", len(response.GetPayload().GetBody()), size)
			}
		}
		if closeErr := stream.CloseSend(); closeErr != nil {
			t.Fatalf("close bidirectional send: %v", closeErr)
		}
		if _, recvErr := stream.Recv(); recvErr != io.EOF {
			t.Fatalf("bidirectional terminal error = %v, want EOF", recvErr)
		}
	})

	assertGRPCInspectionEvents(t, routes, requestEvents, output)
}

func assertGRPCInspectionEvents(
	t *testing.T,
	routes *routingDialer,
	requestEvents <-chan requestEvent,
	output *ChannelSink,
) {
	t.Helper()
	if routes.count("grpc.test:443") == 0 {
		t.Fatal("gRPC traffic did not use the injected dialer")
	}
	methods := make(map[string]bool)
	for range 4 {
		select {
		case event := <-requestEvents:
			if event.protocol != "HTTP/2.0" {
				t.Errorf("gRPC inspection protocol = %s, want HTTP/2.0", event.protocol)
			}
			if event.contentType != "application/grpc" {
				t.Errorf("gRPC content type = %q", event.contentType)
			}
			methods[event.path] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for gRPC inspection event")
		}
	}
	if len(methods) != 4 {
		t.Fatalf("observed gRPC methods = %d, want 4: %#v", len(methods), methods)
	}
	for range 8 {
		select {
		case event := <-output.Events():
			if event.Protocol != ProtocolGRPCS || !event.TLS {
				t.Errorf("gRPC event protocol = %q tls=%t", event.Protocol, event.TLS)
			}
			if event.GRPC == nil || event.GRPC.Service == "" || event.GRPC.Method == "" {
				t.Errorf("gRPC output = %#v", event.GRPC)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for structured gRPC event")
		}
	}
}

type requestEvent struct {
	host        string
	path        string
	protocol    string
	contentType string
}

func newTestHandler(
	t *testing.T,
	ca *tls.Certificate,
	routes *routingDialer,
	events chan<- requestEvent,
	sinks ...Sink,
) *Handler {
	t.Helper()
	var sink Sink
	if len(sinks) != 0 {
		sink = sinks[0]
	}
	handler, err := New(Config{
		CA:          ca,
		DialContext: routes.DialContext,
		AllowHTTP2:  true,
		TLSConfig: &tls.Config{ //nolint:gosec // Test origins use ephemeral self-signed certificates.
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
			NextProtos:         []string{"h2", alpnHTTP1},
		},
		Sink: sink,
		OnRequest: func(request *http.Request) {
			if events == nil {
				return
			}
			events <- requestEvent{
				host:        request.Host,
				path:        request.URL.Path,
				protocol:    request.Proto,
				contentType: request.Header.Get("Content-Type"),
			}
		},
	})
	if err != nil {
		t.Fatalf("create traffic inspection handler: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := handler.Close(); closeErr != nil {
			t.Errorf("close traffic inspection handler: %v", closeErr)
		}
	})
	return handler
}

func newHTTPClient(t *testing.T, handler *Handler, ca *tls.Certificate, target string, allowHTTP2 bool) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(ca.Leaf)
	transport := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return dialThroughInspector(t, t.Context(), handler, target), nil
		},
		ForceAttemptHTTP2: allowHTTP2,
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"h2", alpnHTTP1},
		},
	}
	if !allowHTTP2 {
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
		transport.TLSClientConfig.NextProtos = []string{alpnHTTP1}
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func dialThroughInspector(t *testing.T, ctx context.Context, handler *Handler, target string) net.Conn {
	t.Helper()
	client, inspector := net.Pipe()
	go func() {
		if err := handler.ServeConn(ctx, inspector, target); err != nil &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, net.ErrClosed) {
			t.Errorf("serve inspected connection to %s: %v", target, err)
		}
	}()
	return client
}

func startTCPEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for TCP echo server: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener.Addr().String()
}

type routingDialer struct {
	access sync.Mutex
	routes map[string]string
	calls  map[string]int
}

func newRoutingDialer() *routingDialer {
	return &routingDialer{
		routes: make(map[string]string),
		calls:  make(map[string]int),
	}
}

func (d *routingDialer) add(requested, actual string) {
	d.access.Lock()
	defer d.access.Unlock()
	d.routes[requested] = actual
}

func (d *routingDialer) count(address string) int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.calls[address]
}

func (d *routingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.access.Lock()
	actual, exists := d.routes[address]
	d.calls[address]++
	d.access.Unlock()
	if !exists {
		return nil, &net.DNSError{Name: address, Err: "POC route not found"}
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, actual)
}

type grpcPOCServer struct {
	grpc_testing.UnimplementedTestServiceServer
}

func (grpcPOCServer) UnaryCall(_ context.Context, request *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	return &grpc_testing.SimpleResponse{Payload: request.GetPayload()}, nil
}

func (grpcPOCServer) StreamingOutputCall(
	request *grpc_testing.StreamingOutputCallRequest,
	stream grpc.ServerStreamingServer[grpc_testing.StreamingOutputCallResponse],
) error {
	for _, parameter := range request.GetResponseParameters() {
		response := &grpc_testing.StreamingOutputCallResponse{
			Payload: &grpc_testing.Payload{Body: make([]byte, parameter.GetSize())},
		}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
	return nil
}

func (grpcPOCServer) StreamingInputCall(
	stream grpc.ClientStreamingServer[grpc_testing.StreamingInputCallRequest, grpc_testing.StreamingInputCallResponse],
) error {
	var size int32
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&grpc_testing.StreamingInputCallResponse{AggregatedPayloadSize: size})
		}
		if err != nil {
			return err
		}
		size += int32(len(request.GetPayload().GetBody()))
	}
}

func (grpcPOCServer) FullDuplexCall(
	stream grpc.BidiStreamingServer[grpc_testing.StreamingOutputCallRequest, grpc_testing.StreamingOutputCallResponse],
) error {
	for {
		request, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		for _, parameter := range request.GetResponseParameters() {
			if err := stream.Send(&grpc_testing.StreamingOutputCallResponse{
				Payload: &grpc_testing.Payload{Body: make([]byte, parameter.GetSize())},
			}); err != nil {
				return err
			}
		}
	}
}

func newTestCertificate(t *testing.T, commonName string, isCA bool) *tls.Certificate {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate certificate key: %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("generate certificate serial: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
		IsCA:         isCA,
	}
	if isCA {
		template.KeyUsage |= x509.KeyUsageCertSign
		template.BasicConstraintsValid = true
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        leaf,
	}
}

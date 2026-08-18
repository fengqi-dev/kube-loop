//go:build integration

package trafficinspect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	httpBingoHTTPAddress  = "httpbingo.org:80"
	httpBingoHTTPSAddress = "httpbingo.org:443"
	grpcBinH2CAddress     = "grpcb.in:9000"
	grpcBinTLSAddress     = "grpcb.in:9001"
)

func TestHandler_HTTPBingoLive(t *testing.T) {
	authority := newLiveAuthority(t)
	dialer := newDirectRecordingDialer()
	events := make(chan requestEvent, 8)
	handler := newLiveHandler(t, authority, dialer, events)
	tests := []struct {
		name         string
		url          string
		target       string
		allowHTTP2   bool
		wantProtocol int
	}{
		{
			name:         "plain HTTP/1.1",
			url:          "http://httpbingo.org/get?mode=plain-http",
			target:       httpBingoHTTPAddress,
			wantProtocol: 1,
		},
		{
			name:         "TLS HTTP/1.1",
			url:          "https://httpbingo.org/get?mode=tls-http1",
			target:       httpBingoHTTPSAddress,
			wantProtocol: 1,
		},
		{
			name:         "TLS HTTP/2",
			url:          "https://httpbingo.org/get?mode=tls-http2",
			target:       httpBingoHTTPSAddress,
			allowHTTP2:   true,
			wantProtocol: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runHTTPBingoCase(t, handler, authority, test.url, test.target, test.allowHTTP2, test.wantProtocol)
		})
	}
	if dialer.count(httpBingoHTTPAddress) == 0 || dialer.count(httpBingoHTTPSAddress) == 0 {
		t.Fatalf("HTTPBingo dial counts: http=%d https=%d", dialer.count(httpBingoHTTPAddress), dialer.count(httpBingoHTTPSAddress))
	}
	assertLiveEvents(t, events, len(tests), map[string]bool{
		"httpbingo.org/get": true,
	})
}

func runHTTPBingoCase(
	t *testing.T,
	handler *Handler,
	authority *tls.Certificate,
	url, target string,
	allowHTTP2 bool,
	wantProtocol int,
) {
	t.Helper()
	client := newHTTPClient(t, handler, authority, target, allowHTTP2)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create HTTPBingo request: %v", err)
	}
	request.Header.Set("X-KubeLoop-POC", "traffic-inspection")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("call HTTPBingo: %v", err)
	}
	var payload struct {
		Arguments map[string][]string `json:"args"`
		Method    string              `json:"method"`
		URL       string              `json:"url"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&payload)
	closeErr := response.Body.Close()
	if decodeErr != nil {
		t.Fatalf("decode HTTPBingo response: %v", decodeErr)
	}
	if closeErr != nil {
		t.Fatalf("close HTTPBingo response: %v", closeErr)
	}
	if response.StatusCode != http.StatusOK || response.ProtoMajor != wantProtocol {
		t.Fatalf("HTTPBingo response: status=%d protocol=%s", response.StatusCode, response.Proto)
	}
	if payload.Method != http.MethodGet || len(payload.Arguments["mode"]) != 1 {
		t.Fatalf("unexpected HTTPBingo payload: %#v", payload)
	}
	if !strings.HasPrefix(payload.URL, strings.TrimSuffix(url, payload.Arguments["mode"][0])) {
		t.Fatalf("HTTPBingo URL = %q, request = %q", payload.URL, url)
	}
}

func TestHandler_GRPCBinLive(t *testing.T) {
	authority := newLiveAuthority(t)
	dialer := newDirectRecordingDialer()
	events := make(chan requestEvent, 16)
	handler := newLiveHandler(t, authority, dialer, events)
	messages := newGRPCBinMessages(t)
	tests := []struct {
		name        string
		target      string
		credentials credentials.TransportCredentials
	}{
		{
			name:        "h2c port 9000",
			target:      grpcBinH2CAddress,
			credentials: insecure.NewCredentials(),
		},
		{
			name:        "TLS port 9001",
			target:      grpcBinTLSAddress,
			credentials: liveGRPCCredentials(authority),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runGRPCBinCase(t, handler, messages, test.target, test.credentials)
		})
	}
	for _, target := range []string{grpcBinH2CAddress, grpcBinTLSAddress} {
		if dialer.count(target) == 0 {
			t.Errorf("grpcb.in traffic did not use injected dialer for %s", target)
		}
	}
	assertLiveEvents(t, events, len(tests)*4, map[string]bool{
		grpcBinH2CAddress + "/grpcbin.GRPCBin/": true,
		grpcBinTLSAddress + "/grpcbin.GRPCBin/": true,
	})
}

func runGRPCBinCase(
	t *testing.T,
	handler *Handler,
	messages grpcBinMessages,
	target string,
	transportCredentials credentials.TransportCredentials,
) {
	t.Helper()
	runGRPCBinWithDialer(
		t,
		messages,
		target,
		transportCredentials,
		func(_ context.Context, _ string) (net.Conn, error) {
			return dialThroughInspector(t, t.Context(), handler, target), nil
		},
	)
}

func runGRPCBinWithDialer(
	t *testing.T,
	messages grpcBinMessages,
	target string,
	transportCredentials credentials.TransportCredentials,
	dialContext func(context.Context, string) (net.Conn, error),
) {
	t.Helper()
	connection, err := grpc.NewClient(
		"passthrough:///"+target,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithContextDialer(dialContext),
	)
	if err != nil {
		t.Fatalf("create grpcb.in client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := connection.Close(); closeErr != nil {
			t.Errorf("close grpcb.in client: %v", closeErr)
		}
	})

	callGRPCBinUnary(t, connection, messages)
	callGRPCBinServerStream(t, connection, messages)
	callGRPCBinClientStream(t, connection, messages)
	callGRPCBinBidirectional(t, connection, messages)
}

func callGRPCBinUnary(t *testing.T, connection *grpc.ClientConn, messages grpcBinMessages) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	response := messages.newDummy("")
	if err := connection.Invoke(
		ctx,
		"/grpcbin.GRPCBin/DummyUnary",
		messages.newDummy("kubeloop-unary"),
		response,
	); err != nil {
		t.Fatalf("grpcb.in unary call: %v", err)
	}
	messages.assertValue(t, response, "kubeloop-unary")
}

func callGRPCBinServerStream(t *testing.T, connection *grpc.ClientConn, messages grpcBinMessages) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stream, err := connection.NewStream(
		ctx,
		&grpc.StreamDesc{ServerStreams: true},
		"/grpcbin.GRPCBin/DummyServerStream",
	)
	if err != nil {
		t.Fatalf("start grpcb.in server stream: %v", err)
	}
	if err := stream.SendMsg(messages.newDummy("kubeloop-server-stream")); err != nil {
		t.Fatalf("send grpcb.in server stream request: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close grpcb.in server stream send: %v", err)
	}
	count := 0
	for {
		response := messages.newDummy("")
		err := stream.RecvMsg(response)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("receive grpcb.in server stream: %v", err)
		}
		messages.assertValue(t, response, "kubeloop-server-stream")
		count++
	}
	if count != 10 {
		t.Fatalf("grpcb.in server stream responses = %d, want 10", count)
	}
}

func callGRPCBinClientStream(t *testing.T, connection *grpc.ClientConn, messages grpcBinMessages) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stream, err := connection.NewStream(
		ctx,
		&grpc.StreamDesc{ClientStreams: true},
		"/grpcbin.GRPCBin/DummyClientStream",
	)
	if err != nil {
		t.Fatalf("start grpcb.in client stream: %v", err)
	}
	for index := range 10 {
		if err := stream.SendMsg(messages.newDummy(fmt.Sprintf("kubeloop-client-%d", index))); err != nil {
			t.Fatalf("send grpcb.in client stream item %d: %v", index, err)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close grpcb.in client stream send: %v", err)
	}
	response := messages.newDummy("")
	if err := stream.RecvMsg(response); err != nil {
		t.Fatalf("receive grpcb.in client stream response: %v", err)
	}
	messages.assertValue(t, response, "kubeloop-client-9")
}

func callGRPCBinBidirectional(t *testing.T, connection *grpc.ClientConn, messages grpcBinMessages) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stream, err := connection.NewStream(
		ctx,
		&grpc.StreamDesc{ClientStreams: true, ServerStreams: true},
		"/grpcbin.GRPCBin/DummyBidirectionalStreamStream",
	)
	if err != nil {
		t.Fatalf("start grpcb.in bidirectional stream: %v", err)
	}
	for index := range 2 {
		value := fmt.Sprintf("kubeloop-bidi-%d", index)
		if err := stream.SendMsg(messages.newDummy(value)); err != nil {
			t.Fatalf("send grpcb.in bidirectional item %d: %v", index, err)
		}
		response := messages.newDummy("")
		if err := stream.RecvMsg(response); err != nil {
			t.Fatalf("receive grpcb.in bidirectional item %d: %v", index, err)
		}
		messages.assertValue(t, response, value)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close grpcb.in bidirectional stream: %v", err)
	}
}

type grpcBinMessages struct {
	descriptor  protoreflect.MessageDescriptor
	stringField protoreflect.FieldDescriptor
}

func newGRPCBinMessages(t *testing.T) grpcBinMessages {
	t.Helper()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("grpcbin-live-poc.proto"),
		Package: proto.String("grpcbin"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("DummyMessage"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:   proto.String("f_string"),
				Number: proto.Int32(1),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("build grpcb.in dynamic descriptor: %v", err)
	}
	descriptor := file.Messages().ByName("DummyMessage")
	return grpcBinMessages{
		descriptor:  descriptor,
		stringField: descriptor.Fields().ByName("f_string"),
	}
}

func (m grpcBinMessages) newDummy(value string) *dynamicpb.Message {
	message := dynamicpb.NewMessage(m.descriptor)
	if value != "" {
		message.Set(m.stringField, protoreflect.ValueOfString(value))
	}
	return message
}

func (m grpcBinMessages) assertValue(t *testing.T, message *dynamicpb.Message, want string) {
	t.Helper()
	if got := message.Get(m.stringField).String(); got != want {
		t.Fatalf("grpcb.in value = %q, want %q", got, want)
	}
}

func newLiveAuthority(t *testing.T) *tls.Certificate {
	t.Helper()
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create live-test authority: %v", err)
	}
	return authority.TLSCertificate()
}

func newLiveHandler(
	t *testing.T,
	authority *tls.Certificate,
	dialer *directRecordingDialer,
	events chan<- requestEvent,
) *Handler {
	t.Helper()
	handler, err := New(Config{
		CA:          authority,
		DialContext: dialer.DialContext,
		AllowHTTP2:  true,
		OnRequest: func(request *http.Request) {
			events <- requestEvent{
				host:        request.Host,
				path:        request.URL.Path,
				protocol:    request.Proto,
				contentType: request.Header.Get("Content-Type"),
			}
		},
	})
	if err != nil {
		t.Fatalf("create live traffic inspection handler: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := handler.Close(); closeErr != nil {
			t.Errorf("close live traffic inspection handler: %v", closeErr)
		}
	})
	return handler
}

func liveGRPCCredentials(authority *tls.Certificate) credentials.TransportCredentials {
	roots := x509.NewCertPool()
	roots.AddCert(authority.Leaf)
	return credentials.NewTLS(&tls.Config{
		RootCAs:    roots,
		ServerName: "grpcb.in",
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2"},
	})
}

func assertLiveEvents(
	t *testing.T,
	events <-chan requestEvent,
	wantCount int,
	wantPrefixes map[string]bool,
) {
	t.Helper()
	seenPrefixes := make(map[string]bool)
	for range wantCount {
		select {
		case event := <-events:
			if strings.HasPrefix(event.contentType, "application/grpc") && event.protocol != "HTTP/2.0" {
				t.Errorf("gRPC event protocol = %s, want HTTP/2.0", event.protocol)
			}
			key := event.host + event.path
			for prefix := range wantPrefixes {
				if strings.HasPrefix(key, prefix) {
					seenPrefixes[prefix] = true
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out after %d live inspection events", len(seenPrefixes))
		}
	}
	for prefix := range wantPrefixes {
		if !seenPrefixes[prefix] {
			t.Errorf("did not observe live inspection event prefix %q", prefix)
		}
	}
}

type directRecordingDialer struct {
	access sync.Mutex
	calls  map[string]int
}

func newDirectRecordingDialer() *directRecordingDialer {
	return &directRecordingDialer{calls: make(map[string]int)}
}

func (d *directRecordingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.access.Lock()
	d.calls[address]++
	d.access.Unlock()
	dialer := net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, address)
}

func (d *directRecordingDialer) count(address string) int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.calls[address]
}

// Package trafficinspect contains the proof-of-concept transparent HTTP
// inspection engine used to evaluate an in-process sing-box integration.
package trafficinspect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
	"golang.org/x/net/http2"
)

const (
	connectEstablished   = "HTTP/1.0 200 OK\r\n\r\n"
	alpnHTTP1            = "http/1.1"
	protocolSniffTimeout = 500 * time.Millisecond
)

var inspectableCleartextPrefaces = [][]byte{
	[]byte("CONNECT "),
	[]byte("DELETE "),
	[]byte("GET "),
	[]byte("HEAD "),
	[]byte("OPTIONS "),
	[]byte("PATCH "),
	[]byte("POST "),
	[]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"),
	[]byte("PUT "),
	[]byte("TRACE "),
}

// DialContextFunc opens an upstream connection. The production implementation
// is expected to use the KubeLoop relay rather than the host network.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Config configures a transparent inspection handler.
type Config struct {
	CA          *tls.Certificate
	DialContext DialContextFunc
	Enabled     func() bool
	OnRequest   func(*http.Request)
	OnResponse  func(*http.Response)
	Sink        Sink
	Policy      CapturePolicy
	OnSinkError func(error)
	TLSConfig   *tls.Config
	AllowHTTP2  bool
}

// Handler adapts goproxy's explicit CONNECT API to an already accepted
// transparent connection whose original destination is known by sing-box.
type Handler struct {
	proxy       *goproxy.ProxyHttpServer
	transport   *http.Transport
	h2c         *http2.Transport
	dialContext DialContextFunc
	enabled     func() bool
}

type upstreamRoundTripper struct {
	http *http.Transport
	h2c  *http2.Transport
}

func (t upstreamRoundTripper) RoundTrip(
	request *http.Request,
	_ *goproxy.ProxyCtx,
) (*http.Response, error) {
	if request.URL.Scheme == "http" && request.ProtoMajor == 2 {
		return t.h2c.RoundTrip(request)
	}
	return t.http.RoundTrip(request)
}

// New constructs a transparent inspection handler.
func New(config Config) (*Handler, error) {
	if config.CA == nil {
		return nil, errors.New("traffic inspection CA is required")
	}
	if config.DialContext == nil {
		return nil, errors.New("traffic inspection dialer is required")
	}

	tlsConfig := &tls.Config{ //nolint:gosec // Verification policy is supplied by the caller.
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", alpnHTTP1},
	}
	if config.TLSConfig != nil {
		tlsConfig = config.TLSConfig.Clone()
	}
	transport := &http.Transport{
		DialContext:       config.DialContext,
		ForceAttemptHTTP2: config.AllowHTTP2,
		TLSClientConfig:   tlsConfig,
	}
	h2cTransport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
			return config.DialContext(ctx, network, address)
		},
	}
	roundTripper := upstreamRoundTripper{http: transport, h2c: h2cTransport}

	proxy := goproxy.NewProxyHttpServer()
	proxy.AllowHTTP2 = config.AllowHTTP2
	proxy.Tr = transport
	proxy.ConnectDialWithReq = func(request *http.Request, network, address string) (net.Conn, error) {
		return config.DialContext(request.Context(), network, address)
	}

	mitm := &goproxy.ConnectAction{
		Action:    goproxy.ConnectMitm,
		TLSConfig: goproxy.TLSConfigFromCA(config.CA),
	}
	proxy.OnRequest().HandleConnectFunc(
		func(host string, proxyContext *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			proxyContext.UserData = canonicalAuthority(host, "https")
			return mitm, host
		},
	)
	proxy.OnRequest().DoFunc(func(request *http.Request, proxyContext *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		proxyContext.RoundTripper = roundTripper
		originalAuthority, _ := proxyContext.UserData.(string)
		if originalAuthority != "" {
			if response := enforceOriginalAuthority(request, originalAuthority); response != nil {
				return request, response
			}
		}
		protocol := classifyProtocol(request)
		trace := requestTrace{
			flowID:      newEventID(),
			started:     time.Now(),
			protocol:    protocol,
			destination: originalAuthority,
		}
		if trace.destination == "" {
			trace.destination = canonicalAuthority(request.Host, request.URL.Scheme)
		}
		request = request.WithContext(context.WithValue(request.Context(), requestTraceKey{}, trace))
		wrapRequestBody(request, trace, config)
		emitEvent(request.Context(), config, buildRequestEvent(request, trace))
		if config.OnRequest != nil {
			config.OnRequest(request)
		}
		return request, nil
	})
	proxy.OnResponse().DoFunc(func(response *http.Response, _ *goproxy.ProxyCtx) *http.Response {
		if response != nil && response.Request != nil {
			trace := responseTrace(response)
			wrapResponseBody(response, trace, config)
			emitEvent(response.Request.Context(), config, buildResponseEvent(response, trace))
		}
		if response != nil && config.OnResponse != nil {
			config.OnResponse(response)
		}
		return response
	})

	return &Handler{
		proxy: proxy, transport: transport, h2c: h2cTransport,
		dialContext: config.DialContext, enabled: config.Enabled,
	}, nil
}

func buildRequestEvent(request *http.Request, trace requestTrace) Event {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		ID:            newEventID(),
		FlowID:        trace.flowID,
		Timestamp:     trace.started,
		Type:          EventTypeRequest,
		Protocol:      trace.protocol,
		TLS:           isTLSProtocol(trace.protocol),
		Destination:   trace.destination,
		HTTP: &HTTPEvent{
			Version:        request.Proto,
			Method:         request.Method,
			Host:           request.Host,
			Path:           request.URL.RequestURI(),
			RequestHeaders: request.Header.Clone(),
		},
		Raw: rawHTTPHeader(request, nil, directionRequest),
	}
	if trace.protocol == ProtocolGRPC || trace.protocol == ProtocolGRPCS {
		service, method := splitGRPCPath(request.URL.Path)
		event.GRPC = &GRPCEvent{Service: service, Method: method, Path: request.URL.Path}
	}
	return event
}

func responseTrace(response *http.Response) requestTrace {
	now := time.Now()
	trace, found := response.Request.Context().Value(requestTraceKey{}).(requestTrace)
	if !found {
		trace = requestTrace{
			flowID:      newEventID(),
			started:     now,
			protocol:    classifyProtocol(response.Request),
			destination: canonicalAuthority(response.Request.Host, response.Request.URL.Scheme),
		}
	}
	return trace
}

func buildResponseEvent(response *http.Response, trace requestTrace) Event {
	now := time.Now()
	event := Event{
		SchemaVersion: EventSchemaVersion,
		ID:            newEventID(),
		FlowID:        trace.flowID,
		Timestamp:     now,
		Type:          EventTypeResponse,
		Protocol:      trace.protocol,
		TLS:           isTLSProtocol(trace.protocol),
		Destination:   trace.destination,
		Duration:      now.Sub(trace.started).Milliseconds(),
		HTTP: &HTTPEvent{
			Version:         response.Proto,
			Method:          response.Request.Method,
			Host:            response.Request.Host,
			Path:            response.Request.URL.RequestURI(),
			Status:          response.StatusCode,
			ResponseHeaders: response.Header.Clone(),
		},
		Raw: rawHTTPHeader(nil, response, directionResponse),
	}
	if trace.protocol == ProtocolGRPC || trace.protocol == ProtocolGRPCS {
		service, method := splitGRPCPath(response.Request.URL.Path)
		grpcStatus := response.Header.Get("Grpc-Status")
		if grpcStatus == "" {
			grpcStatus = response.Trailer.Get("Grpc-Status")
		}
		event.GRPC = &GRPCEvent{
			Service: service,
			Method:  method,
			Path:    response.Request.URL.Path,
			Status:  grpcStatus,
		}
	}
	return event
}

func emitEvent(ctx context.Context, config Config, event Event) {
	if config.Sink == nil {
		return
	}
	if err := config.Sink.Emit(ctx, event); err != nil && config.OnSinkError != nil {
		config.OnSinkError(err)
	}
}

// Close releases idle upstream connections.
func (h *Handler) Close() error {
	h.transport.CloseIdleConnections()
	h.h2c.CloseIdleConnections()
	return nil
}

// ServeConn takes ownership of connection and transparently inspects traffic
// addressed to target. It returns after the connection closes or ctx is
// canceled.
func (h *Handler) ServeConn(ctx context.Context, connection net.Conn, target string) error {
	if connection == nil {
		return errors.New("traffic inspection connection is required")
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		_ = connection.Close()
		return fmt.Errorf("parse traffic inspection target: %w", err)
	}
	if h.enabled != nil && !h.enabled() {
		return h.relayUninspected(ctx, connection, target)
	}
	reader := bufio.NewReader(connection)
	if !sniffInspectableProtocol(connection, reader) {
		return h.relayUninspected(ctx, &bufferedConn{Conn: connection, reader: reader}, target)
	}

	tracked := newTransparentConn(&bufferedConn{Conn: connection, reader: reader})
	request := (&http.Request{
		Method: http.MethodConnect,
		URL: &url.URL{
			Host:   target,
			Opaque: target,
		},
		Host:       target,
		Header:     make(http.Header),
		RemoteAddr: connection.RemoteAddr().String(),
	}).WithContext(ctx)
	writer := &connectionResponseWriter{
		connection: tracked,
		header:     make(http.Header),
	}

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		h.proxy.ServeHTTP(writer, request)
	}()

	select {
	case <-tracked.done:
		return nil
	case <-ctx.Done():
		if err := tracked.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("close canceled traffic inspection connection: %w", err)
		}
		return context.Cause(ctx)
	case <-serveDone:
		select {
		case <-tracked.done:
			return nil
		case <-ctx.Done():
			if err := tracked.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("close canceled traffic inspection connection: %w", err)
			}
			return context.Cause(ctx)
		}
	}
}

func sniffInspectableProtocol(connection net.Conn, reader *bufio.Reader) bool {
	deadline := time.Now().Add(protocolSniffTimeout)
	if err := connection.SetReadDeadline(deadline); err == nil {
		defer func() { _ = connection.SetReadDeadline(time.Time{}) }()
	}

	for length := 1; length <= len("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); length++ {
		prefix, err := reader.Peek(length)
		inspectable, possible := classifyInspectablePrefix(prefix)
		if inspectable {
			return true
		}
		if !possible || err != nil {
			return false
		}
	}
	return false
}

func classifyInspectablePrefix(prefix []byte) (inspectable, possible bool) {
	if len(prefix) == 0 {
		return false, true
	}
	for _, preface := range inspectableCleartextPrefaces {
		if bytes.Equal(prefix, preface) {
			return true, true
		}
		if len(prefix) < len(preface) && bytes.Equal(prefix, preface[:len(prefix)]) {
			possible = true
		}
	}
	if tlsClientHelloPrefix(prefix) {
		if len(prefix) == 6 {
			return true, true
		}
		possible = true
	}
	return false, possible
}

func tlsClientHelloPrefix(prefix []byte) bool {
	if len(prefix) > 6 || prefix[0] != 0x16 {
		return false
	}
	if len(prefix) >= 2 && prefix[1] != 0x03 {
		return false
	}
	if len(prefix) >= 3 && (prefix[2] < 0x01 || prefix[2] > 0x04) {
		return false
	}
	if len(prefix) >= 5 && prefix[3] == 0 && prefix[4] == 0 {
		return false
	}
	return len(prefix) < 6 || prefix[5] == 0x01
}

func (h *Handler) relayUninspected(ctx context.Context, client net.Conn, target string) error {
	upstream, err := h.dialContext(ctx, "tcp", target)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("dial uninspected traffic target %s: %w", target, err)
	}
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
	}()

	done := make(chan struct{})
	go func() {
		streamcopy.Bidirectional(client, upstream)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = client.Close()
		_ = upstream.Close()
		<-done
		return context.Cause(ctx)
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(payload []byte) (int, error) {
	return c.reader.Read(payload)
}

func (c *bufferedConn) CloseWrite() error {
	if writer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return c.Conn.Close()
}

func enforceOriginalAuthority(request *http.Request, originalAuthority string) *http.Response {
	if canonicalAuthority(request.Host, request.URL.Scheme) == originalAuthority {
		return nil
	}
	if !hasExplicitPort(request.Host) && authorityHost(request.Host) == authorityHost(originalAuthority) {
		request.Host = originalAuthority
		request.URL.Host = originalAuthority
		return nil
	}
	return goproxy.NewResponse(
		request,
		"text/plain",
		http.StatusMisdirectedRequest,
		"request authority does not match the original destination",
	)
}

func hasExplicitPort(authority string) bool {
	_, _, err := net.SplitHostPort(authority)
	return err == nil
}

func authorityHost(authority string) string {
	host, _, err := net.SplitHostPort(authority)
	if err != nil {
		host = strings.Trim(authority, "[]")
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func canonicalAuthority(authority, scheme string) string {
	host, port, err := net.SplitHostPort(authority)
	if err == nil {
		return strings.ToLower(net.JoinHostPort(strings.TrimSuffix(host, "."), port))
	}
	host = strings.TrimSuffix(strings.Trim(authority, "[]"), ".")
	if strings.EqualFold(scheme, "http") {
		port = "80"
	} else {
		port = "443"
	}
	return strings.ToLower(net.JoinHostPort(host, port))
}

type transparentConn struct {
	net.Conn
	done          chan struct{}
	closeOnce     sync.Once
	writeAccess   sync.Mutex
	suppressReply bool
}

func newTransparentConn(connection net.Conn) *transparentConn {
	return &transparentConn{
		Conn:          connection,
		done:          make(chan struct{}),
		suppressReply: true,
	}
}

func (c *transparentConn) Write(payload []byte) (int, error) {
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()
	if c.suppressReply && bytes.Equal(payload, []byte(connectEstablished)) {
		c.suppressReply = false
		return len(payload), nil
	}
	c.suppressReply = false
	return c.Conn.Write(payload)
}

func (c *transparentConn) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		closeErr = c.Conn.Close()
		close(c.done)
	})
	return closeErr
}

type connectionResponseWriter struct {
	connection *transparentConn
	header     http.Header
}

func (w *connectionResponseWriter) Header() http.Header {
	return w.header
}

func (w *connectionResponseWriter) Write(payload []byte) (int, error) {
	return w.connection.Write(payload)
}

func (w *connectionResponseWriter) WriteHeader(statusCode int) {
	if _, err := fmt.Fprintf(w.connection, "HTTP/1.1 %d %s\r\n\r\n", statusCode, http.StatusText(statusCode)); err != nil {
		_ = w.connection.Close()
	}
}

func (w *connectionResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	reader := bufio.NewReader(w.connection)
	writer := bufio.NewWriter(w.connection)
	return w.connection, bufio.NewReadWriter(reader, writer), nil
}

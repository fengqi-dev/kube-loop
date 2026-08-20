// Package websocket provides the repository's context-aware WebSocket API on
// top of github.com/gorilla/websocket.
package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	gorilla "github.com/gorilla/websocket"
)

type MessageType = int

const (
	MessageText   MessageType = gorilla.TextMessage
	MessageBinary MessageType = gorilla.BinaryMessage
)

type StatusCode = int

const (
	StatusNormalClosure           StatusCode = gorilla.CloseNormalClosure
	StatusGoingAway               StatusCode = gorilla.CloseGoingAway
	StatusProtocolError           StatusCode = gorilla.CloseProtocolError
	StatusUnsupportedData         StatusCode = gorilla.CloseUnsupportedData
	StatusNoStatusRcvd            StatusCode = gorilla.CloseNoStatusReceived
	StatusAbnormalClosure         StatusCode = gorilla.CloseAbnormalClosure
	StatusInvalidFramePayloadData StatusCode = gorilla.CloseInvalidFramePayloadData
	StatusPolicyViolation         StatusCode = gorilla.ClosePolicyViolation
	StatusMessageTooBig           StatusCode = gorilla.CloseMessageTooBig
	StatusMandatoryExtension      StatusCode = gorilla.CloseMandatoryExtension
	StatusInternalError           StatusCode = gorilla.CloseInternalServerErr
	StatusServiceRestart          StatusCode = gorilla.CloseServiceRestart
	StatusTryAgainLater           StatusCode = gorilla.CloseTryAgainLater
	StatusBadGateway              StatusCode = 1014
	StatusTLSHandshake            StatusCode = gorilla.CloseTLSHandshake
)

type CompressionMode int

const (
	CompressionDisabled CompressionMode = iota
	CompressionContextTakeover
	CompressionNoContextTakeover
)

type DialOptions struct {
	HTTPClient           *http.Client
	HTTPHeader           http.Header
	Host                 string
	Subprotocols         []string
	CompressionMode      CompressionMode
	CompressionThreshold int
}

type AcceptOptions struct {
	Subprotocols         []string
	InsecureSkipVerify   bool
	OriginPatterns       []string
	CompressionMode      CompressionMode
	CompressionThreshold int
}

// Conn serializes Gorilla writes while retaining its single-reader contract.
type Conn struct {
	raw *gorilla.Conn

	readMu  sync.Mutex
	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

func Dial(ctx context.Context, endpoint string, options *DialOptions) (*Conn, *http.Response, error) {
	if ctx == nil {
		return nil, nil, errors.New("WebSocket dial context is required")
	}
	dialer := *gorilla.DefaultDialer
	var header http.Header
	if options != nil {
		header = options.HTTPHeader.Clone()
		dialer.Subprotocols = append([]string(nil), options.Subprotocols...)
		dialer.EnableCompression = options.CompressionMode != CompressionDisabled
		if options.Host != "" {
			if header == nil {
				header = make(http.Header)
			}
			header.Set("Host", options.Host)
		}
		if options.HTTPClient != nil {
			var cancel context.CancelFunc
			if options.HTTPClient.Timeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, options.HTTPClient.Timeout)
				defer cancel()
			}
			if err := configureDialerTransport(&dialer, options.HTTPClient.Transport); err != nil {
				return nil, nil, err
			}
			if options.HTTPClient.Jar != nil {
				dialer.Jar = options.HTTPClient.Jar
			}
		}
	}
	connection, response, err := dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		return nil, response, fmt.Errorf("dial WebSocket: %w", err)
	}
	if response != nil && response.TLS == nil {
		if tlsConnection, ok := connection.NetConn().(*tls.Conn); ok {
			state := tlsConnection.ConnectionState()
			response.TLS = &state
		}
	}
	return newConn(connection), response, nil
}

func configureDialerTransport(dialer *gorilla.Dialer, roundTripper http.RoundTripper) error {
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	transport, ok := roundTripper.(*http.Transport)
	if !ok {
		return fmt.Errorf("WebSocket HTTP client transport %T is unsupported", roundTripper)
	}
	dialer.Proxy = transport.Proxy
	dialer.NetDialContext = transport.DialContext
	dialer.NetDialTLSContext = transport.DialTLSContext
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		// Gorilla implements the HTTP/1.1 Upgrade handshake, not RFC 8441
		// extended CONNECT. Do not inherit h2 ALPN preferences from the HTTP
		// transport or TLS may negotiate a protocol this dialer cannot speak.
		dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	return nil
}

func Accept(writer http.ResponseWriter, request *http.Request, options *AcceptOptions) (*Conn, error) {
	upgrader := gorilla.Upgrader{}
	if options != nil {
		upgrader.Subprotocols = append([]string(nil), options.Subprotocols...)
		upgrader.EnableCompression = options.CompressionMode != CompressionDisabled
		upgrader.CheckOrigin = originChecker(options)
	}
	responseHeader := writer.Header().Clone()
	connection, err := upgrader.Upgrade(writer, request, responseHeader)
	if err != nil {
		return nil, fmt.Errorf("accept WebSocket: %w", err)
	}
	return newConn(connection), nil
}

func originChecker(options *AcceptOptions) func(*http.Request) bool {
	return func(request *http.Request) bool {
		if options.InsecureSkipVerify {
			return true
		}
		origin := request.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			return false
		}
		if equalHost(parsed.Host, request.Host) {
			return true
		}
		originHost := strings.ToLower(parsed.Host)
		originURL := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
		for _, pattern := range options.OriginPatterns {
			candidate := originHost
			if strings.Contains(pattern, "://") {
				candidate = originURL
			}
			matched, matchErr := path.Match(strings.ToLower(pattern), candidate)
			if matchErr == nil && matched {
				return true
			}
		}
		return false
	}
}

func equalHost(left, right string) bool {
	return strings.EqualFold(stripDefaultPort(left), stripDefaultPort(right))
}

func stripDefaultPort(host string) string {
	name, port, err := net.SplitHostPort(host)
	if err != nil || (port != "80" && port != "443") {
		return host
	}
	return name
}

func newConn(connection *gorilla.Conn) *Conn {
	return &Conn{raw: connection}
}

func (connection *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	if ctx == nil {
		return 0, nil, errors.New("WebSocket read context is required")
	}
	connection.readMu.Lock()
	defer connection.readMu.Unlock()
	if err := prepareContextDeadline(ctx, connection.raw.SetReadDeadline); err != nil {
		return 0, nil, err
	}
	stop := interruptOnCancel(ctx, connection.raw.SetReadDeadline)
	messageType, payload, err := connection.raw.ReadMessage()
	finishContextDeadline(stop, connection.raw.SetReadDeadline)
	if err != nil {
		if ctx.Err() != nil {
			_ = connection.raw.Close()
		}
		return 0, nil, operationError(ctx, err)
	}
	return messageType, payload, nil
}

func (connection *Conn) Write(ctx context.Context, messageType MessageType, payload []byte) error {
	if ctx == nil {
		return errors.New("WebSocket write context is required")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if err := prepareContextDeadline(ctx, connection.raw.SetWriteDeadline); err != nil {
		return err
	}
	stop := interruptOnCancel(ctx, connection.raw.NetConn().SetWriteDeadline)
	err := connection.raw.WriteMessage(messageType, payload)
	finishContextDeadline(stop, connection.raw.NetConn().SetWriteDeadline)
	_ = connection.raw.SetWriteDeadline(time.Time{})
	if err != nil && ctx.Err() != nil {
		_ = connection.raw.Close()
	}
	return operationError(ctx, err)
}

func (connection *Conn) Ping(ctx context.Context) error {
	if ctx == nil {
		return errors.New("WebSocket ping context is required")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	stop := interruptOnCancel(ctx, connection.raw.NetConn().SetWriteDeadline)
	err := connection.raw.WriteControl(gorilla.PingMessage, nil, deadline)
	finishContextDeadline(stop, connection.raw.NetConn().SetWriteDeadline)
	return operationError(ctx, err)
}

func (connection *Conn) Close(code StatusCode, reason string) error {
	connection.closeMu.Lock()
	if connection.closed {
		connection.closeMu.Unlock()
		return nil
	}
	connection.closed = true
	connection.closeMu.Unlock()

	writeErr := connection.raw.WriteControl(
		gorilla.CloseMessage,
		gorilla.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	closeErr := connection.raw.Close()
	return errors.Join(writeErr, closeErr)
}

func (connection *Conn) CloseNow() error {
	connection.closeMu.Lock()
	if connection.closed {
		connection.closeMu.Unlock()
		return nil
	}
	connection.closed = true
	connection.closeMu.Unlock()
	return connection.raw.Close()
}

func (connection *Conn) SetReadLimit(limit int64) {
	if limit < 0 {
		limit = 0
	}
	connection.raw.SetReadLimit(limit)
}

func (connection *Conn) Subprotocol() string {
	return connection.raw.Subprotocol()
}

func CloseStatus(err error) StatusCode {
	var closeError *gorilla.CloseError
	if errors.As(err, &closeError) {
		return closeError.Code
	}
	return -1
}

type deadlineStop struct {
	stop  func() bool
	fired chan struct{}
}

func prepareContextDeadline(ctx context.Context, setDeadline func(time.Time) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	return setDeadline(deadline)
}

func interruptOnCancel(ctx context.Context, setDeadline func(time.Time) error) deadlineStop {
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(fired)
	})
	return deadlineStop{stop: stop, fired: fired}
}

func finishContextDeadline(stop deadlineStop, setDeadline func(time.Time) error) {
	if !stop.stop() {
		<-stop.fired
	}
	_ = setDeadline(time.Time{})
}

func operationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("WebSocket operation: %w", contextErr)
	}
	return err
}

// NetConn exposes binary or text WebSocket messages as a byte stream. Each
// Write remains one WebSocket message; reads may span multiple Read calls.
func NetConn(ctx context.Context, connection *Conn, messageType MessageType) net.Conn {
	if ctx == nil {
		ctx = context.Background()
	}
	connection.SetReadLimit(-1)
	stream := &streamConn{ctx: ctx, connection: connection, messageType: messageType}
	stream.stopContext = context.AfterFunc(ctx, func() {
		_ = connection.raw.NetConn().SetDeadline(time.Now())
	})
	return stream
}

type streamConn struct {
	ctx           context.Context
	connection    *Conn
	messageType   MessageType
	reader        io.Reader
	readEOF       bool
	deadlineMu    sync.Mutex
	writeDeadline time.Time
	closeOnce     sync.Once
	closeErr      error
	stopContext   func() bool
}

func (stream *streamConn) Read(payload []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	stream.connection.readMu.Lock()
	defer stream.connection.readMu.Unlock()
	for {
		if stream.readEOF {
			return 0, io.EOF
		}
		if stream.reader == nil {
			messageType, reader, err := stream.connection.raw.NextReader()
			if err != nil {
				if contextErr := stream.ctx.Err(); contextErr != nil {
					return 0, contextErr
				}
				if code := CloseStatus(err); code == StatusNormalClosure || code == StatusGoingAway {
					stream.readEOF = true
					return 0, io.EOF
				}
				return 0, err
			}
			if messageType != stream.messageType {
				reason := fmt.Sprintf("unexpected WebSocket message type %d", messageType)
				_ = stream.connection.Close(StatusUnsupportedData, reason)
				return 0, errors.New(reason)
			}
			stream.reader = reader
		}
		n, err := stream.reader.Read(payload)
		if errors.Is(err, io.EOF) {
			stream.reader = nil
			if n == 0 {
				continue
			}
			return n, nil
		}
		return n, err
	}
}

func (stream *streamConn) Write(payload []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	stream.connection.writeMu.Lock()
	defer stream.connection.writeMu.Unlock()
	stream.deadlineMu.Lock()
	deadline := stream.writeDeadline
	stream.deadlineMu.Unlock()
	if err := stream.connection.raw.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	if err := stream.connection.raw.WriteMessage(stream.messageType, payload); err != nil {
		if contextErr := stream.ctx.Err(); contextErr != nil {
			return 0, contextErr
		}
		return 0, err
	}
	return len(payload), nil
}

func (stream *streamConn) Close() error {
	stream.closeOnce.Do(func() {
		if stream.stopContext != nil {
			stream.stopContext()
		}
		stream.closeErr = stream.connection.Close(StatusNormalClosure, "")
	})
	return stream.closeErr
}

func (stream *streamConn) LocalAddr() net.Addr  { return stream.connection.raw.NetConn().LocalAddr() }
func (stream *streamConn) RemoteAddr() net.Addr { return stream.connection.raw.NetConn().RemoteAddr() }

func (stream *streamConn) SetDeadline(deadline time.Time) error {
	stream.deadlineMu.Lock()
	stream.writeDeadline = deadline
	stream.deadlineMu.Unlock()
	return stream.connection.raw.NetConn().SetDeadline(deadline)
}

func (stream *streamConn) SetReadDeadline(deadline time.Time) error {
	return stream.connection.raw.NetConn().SetReadDeadline(deadline)
}

func (stream *streamConn) SetWriteDeadline(deadline time.Time) error {
	stream.deadlineMu.Lock()
	stream.writeDeadline = deadline
	stream.deadlineMu.Unlock()
	return stream.connection.raw.NetConn().SetWriteDeadline(deadline)
}

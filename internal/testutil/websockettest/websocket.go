package websockettest

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

type DialOptions struct {
	HTTPClient   *http.Client
	HTTPHeader   http.Header
	Subprotocols []string
}

type AcceptOptions struct {
	Subprotocols []string
}

func Dial(ctx context.Context, endpoint string, options *DialOptions) (*websocket.Conn, *http.Response, error) {
	dialer := *websocket.DefaultDialer
	var header http.Header
	if options != nil {
		header = options.HTTPHeader.Clone()
		dialer.Subprotocols = append([]string(nil), options.Subprotocols...)
		if options.HTTPClient != nil {
			roundTripper := options.HTTPClient.Transport
			if roundTripper == nil {
				roundTripper = http.DefaultTransport
			}
			transport, ok := roundTripper.(*http.Transport)
			if !ok {
				return nil, nil, fmt.Errorf("WebSocket HTTP client transport %T is unsupported", roundTripper)
			}
			dialer.Proxy = transport.Proxy
			dialer.NetDialContext = transport.DialContext
			dialer.NetDialTLSContext = transport.DialTLSContext
			if transport.TLSClientConfig != nil {
				dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
				dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
			}
			dialer.Jar = options.HTTPClient.Jar
		}
	}
	connection, response, err := dialer.DialContext(ctx, endpoint, header)
	if err == nil && response != nil && response.TLS == nil {
		if tlsConnection, ok := connection.NetConn().(*tls.Conn); ok {
			state := tlsConnection.ConnectionState()
			response.TLS = &state
		}
	}
	return connection, response, err
}

func Accept(
	writer http.ResponseWriter,
	request *http.Request,
	options *AcceptOptions,
) (*websocket.Conn, error) {
	upgrader := websocket.Upgrader{}
	if options != nil {
		upgrader.Subprotocols = append([]string(nil), options.Subprotocols...)
	}
	connection, err := upgrader.Upgrade(writer, request, writer.Header().Clone())
	if err != nil {
		return nil, fmt.Errorf("accept WebSocket: %w", err)
	}
	return connection, nil
}

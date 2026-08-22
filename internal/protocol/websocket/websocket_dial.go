package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	gorilla "github.com/gorilla/websocket"
)

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

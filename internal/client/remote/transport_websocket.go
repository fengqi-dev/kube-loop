package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/middleware"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) openTaskWebSocket(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	streamPath string,
) (*websocket.Conn, error) {
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("server Profile URL is invalid")
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = remoteWSSScheme
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = streamPath
	endpoint.RawQuery = url.Values{remoteParamNamespace: {current.Namespace}}.Encode()
	credential, err := client.usableCredential(ctx, serverProfile, "")
	if err != nil {
		return nil, err
	}
	connection, status, err := client.dialWebSocket(ctx, endpoint.String(), credential.AccessToken)
	if err == nil {
		return connection, nil
	}
	if status != http.StatusUnauthorized {
		return nil, err
	}
	credential, refreshErr := client.usableCredential(ctx, serverProfile, credential.AccessToken)
	if refreshErr != nil {
		return nil, refreshErr
	}
	connection, _, err = client.dialWebSocket(ctx, endpoint.String(), credential.AccessToken)
	return connection, err
}

func (client *Client) dialWebSocket(
	ctx context.Context,
	endpoint,
	accessToken string,
) (*websocket.Conn, int, error) {
	header := http.Header{"Authorization": {"Bearer " + accessToken}}
	if correlationID := middleware.ID(ctx); correlationID != "" {
		header.Set(middleware.Header, correlationID)
	}
	dialer, err := webSocketDialer(client.httpClient.Transport)
	if err != nil {
		return nil, 0, err
	}
	if client.httpClient.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, client.httpClient.Timeout)
		defer cancel()
	}
	if client.httpClient.Jar != nil {
		dialer.Jar = client.httpClient.Jar
	}
	connection, response, err := dialer.DialContext(ctx, endpoint, header)
	if err == nil {
		if response != nil && response.TLS == nil {
			if tlsConnection, ok := connection.NetConn().(*tls.Conn); ok {
				state := tlsConnection.ConnectionState()
				response.TLS = &state
			}
		}
		return connection, 0, nil
	}
	if response == nil {
		return nil, 0, fmt.Errorf("gateway WebSocket stream failed: %w", err)
	}
	status := response.StatusCode
	contents, readErr := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if bodyErr := errors.Join(readErr, response.Body.Close()); bodyErr != nil {
		return nil, status, fmt.Errorf("read Gateway WebSocket error response: %w", bodyErr)
	}
	return nil, status, decodeAPIError(status, contents)
}

func webSocketDialer(roundTripper http.RoundTripper) (*websocket.Dialer, error) {
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}
	transport, ok := roundTripper.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("WebSocket HTTP client transport %T is unsupported", roundTripper)
	}
	dialer := *websocket.DefaultDialer
	dialer.Proxy = transport.Proxy
	dialer.NetDialContext = transport.DialContext
	dialer.NetDialTLSContext = transport.DialTLSContext
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	return &dialer, nil
}

func readWebSocket(ctx context.Context, connection *websocket.Conn) (int, []byte, error) {
	stop, err := bindWebSocketContext(ctx, connection.SetReadDeadline)
	if err != nil {
		return 0, nil, err
	}
	messageType, payload, err := connection.ReadMessage()
	stop()
	if err != nil && ctx.Err() != nil {
		_ = connection.Close()
		return 0, nil, fmt.Errorf("WebSocket operation: %w", ctx.Err())
	}
	return messageType, payload, err
}

func closeWebSocket(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.Close())
}

func bindWebSocketContext(ctx context.Context, setDeadline func(time.Time) error) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	if err := setDeadline(deadline); err != nil {
		return nil, err
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(fired)
	})
	return func() {
		if !stop() {
			<-fired
		}
		_ = setDeadline(time.Time{})
	}, nil
}

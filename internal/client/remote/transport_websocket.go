package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/correlation"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
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
	if correlationID := correlation.ID(ctx); correlationID != "" {
		header.Set(correlation.Header, correlationID)
	}
	connection, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient:      client.httpClient,
		HTTPHeader:      header,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err == nil {
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

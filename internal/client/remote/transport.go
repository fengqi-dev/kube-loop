package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
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
	connection, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPClient:      client.httpClient,
		HTTPHeader:      http.Header{"Authorization": {"Bearer " + accessToken}},
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

func (client *Client) getJSON(
	ctx context.Context,
	serverProfile profile.Profile,
	requestPath string,
	query url.Values,
	destination any,
) error {
	return client.doJSON(ctx, serverProfile, http.MethodGet, requestPath, query, nil, destination)
}

func (client *Client) doJSON(
	ctx context.Context,
	serverProfile profile.Profile,
	method, requestPath string,
	query url.Values,
	headers http.Header,
	destination any,
) error {
	return client.doJSONBody(ctx, serverProfile, method, requestPath, query, headers, nil, destination)
}

func (client *Client) doJSONBody(
	ctx context.Context,
	serverProfile profile.Profile,
	method, requestPath string,
	query url.Values,
	headers http.Header,
	body []byte,
	destination any,
) error {
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(serverProfile.ID) == "" {
		return errors.New("server Profile ID is required")
	}
	credential, err := client.usableCredential(ctx, serverProfile, "")
	if err != nil {
		return err
	}
	status, response, err := client.request(
		ctx,
		method,
		baseURL,
		requestPath,
		query,
		headers,
		body,
		credential.AccessToken,
	)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		credential, err = client.usableCredential(ctx, serverProfile, credential.AccessToken)
		if err != nil {
			return err
		}
		status, response, err = client.request(
			ctx,
			method,
			baseURL,
			requestPath,
			query,
			headers,
			body,
			credential.AccessToken,
		)
		if err != nil {
			return err
		}
	}
	if status < 200 || status >= 300 {
		return decodeAPIError(status, response)
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("gateway response contains invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("gateway response must contain one JSON document")
	}
	return nil
}

func (client *Client) usableCredential(
	ctx context.Context,
	serverProfile profile.Profile,
	rejectedAccessToken string,
) (credentials.Credential, error) {
	current, err := client.credentials.Get(serverProfile.ID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if rejectedAccessToken == "" && current.AccessExpiresAt.After(client.now().Add(client.refreshAhead)) {
		return current, nil
	}
	client.refreshMu.Lock()
	defer client.refreshMu.Unlock()
	current, err = client.credentials.Get(serverProfile.ID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if rejectedAccessToken != "" && current.AccessToken != rejectedAccessToken {
		return current, nil
	}
	if rejectedAccessToken == "" && current.AccessExpiresAt.After(client.now().Add(client.refreshAhead)) {
		return current, nil
	}
	if !current.RefreshExpiresAt.IsZero() && !current.RefreshExpiresAt.After(client.now()) {
		return credentials.Credential{}, client.expiredLogin(serverProfile.ID)
	}
	refreshed, err := client.refresher.Refresh(ctx, serverProfile.BaseURL, current)
	if err != nil {
		if clientauth.IsInvalidGrant(err) {
			return credentials.Credential{}, client.expiredLogin(serverProfile.ID)
		}
		return credentials.Credential{}, fmt.Errorf("refresh Gateway login: %w", err)
	}
	if err := client.credentials.Set(serverProfile.ID, refreshed); err != nil {
		return credentials.Credential{}, fmt.Errorf("store refreshed Gateway login: %w", err)
	}
	return refreshed, nil
}

func (client *Client) expiredLogin(profileID string) error {
	deleteErr := client.credentials.Delete(profileID)
	if deleteErr != nil && !errors.Is(deleteErr, credentials.ErrNotFound) {
		return errors.Join(
			clientauth.ErrLoginExpired,
			fmt.Errorf("clear expired Gateway login: %w", deleteErr),
		)
	}
	return clientauth.ErrLoginExpired
}

func (client *Client) request(
	ctx context.Context,
	method, baseURL, requestPath string,
	query url.Values,
	headers http.Header,
	body []byte,
	accessToken string,
) (_ int, _ []byte, resultErr error) {
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	endpoint := baseURL + requestPath
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestContext, method, endpoint, requestBody)
	if err != nil {
		return 0, nil, errors.New("create Gateway request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return 0, nil, errors.New("gateway request timed out")
		}
		return 0, nil, fmt.Errorf("gateway request failed: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close Gateway response: %w", err))
		}
	}()
	contents, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return 0, nil, errors.New("read Gateway response")
	}
	if len(contents) > maximumResponseBytes {
		return 0, nil, errors.New("gateway response exceeds 2 MiB")
	}
	return response.StatusCode, contents, nil
}

func decodeAPIError(status int, contents []byte) error {
	document := struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Field     string `json:"field,omitempty"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}{}
	if json.Unmarshal(contents, &document) == nil && document.Error.Code != "" {
		return &APIError{
			Status: status, Code: document.Error.Code, Message: document.Error.Message,
			Field: document.Error.Field, RequestID: document.Error.RequestID,
		}
	}
	return &APIError{Status: status}
}

func generationHeader(generation uint64) http.Header {
	return http.Header{"If-Match": {fmt.Sprintf("\"%d\"", generation)}}
}

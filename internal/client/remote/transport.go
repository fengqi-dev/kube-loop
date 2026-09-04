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

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

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
	if correlationID := utils.CorrelationID(requestContext); correlationID != "" {
		request.Header.Set(middleware.Header, correlationID)
	}
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

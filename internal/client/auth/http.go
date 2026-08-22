package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"errors"
)

func (client *Client) getJSON(ctx context.Context, endpoint string, destination any) error {
	return client.request(ctx, http.MethodGet, endpoint, "", nil, destination)
}

func (client *Client) postForm(ctx context.Context, endpoint string, form url.Values, destination any) error {
	return client.request(
		ctx,
		http.MethodPost,
		endpoint,
		"application/x-www-form-urlencoded",
		[]byte(form.Encode()),
		destination,
	)
}

func (client *Client) request(
	ctx context.Context,
	method,
	endpoint,
	contentType string,
	raw []byte,
	destination any,
) (resultErr error) {
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("create authentication request")
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return errors.New("authentication request timed out")
		}
		return fmt.Errorf("authentication request failed: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close authentication response: %w", err))
		}
	}()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("read authentication response")
	}
	if len(responseRaw) > maxResponseBytes {
		return errors.New("authentication response exceeds 64 KiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response.StatusCode, response.Header.Get("X-Request-ID"), responseRaw)
	}
	if destination == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseRaw))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("authentication response contains invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("authentication response must contain one JSON document")
	}
	return nil
}

func decodeAPIError(status int, requestID string, raw []byte) error {
	var document errorResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err == nil {
		if trailing := decoder.Decode(&struct{}{}); errors.Is(trailing, io.EOF) && document.Code != "" {
			return &APIError{
				Status: status, Code: document.Code, Message: document.Message,
				RequestID: strings.TrimSpace(requestID),
			}
		}
	}
	return &APIError{Status: status, RequestID: strings.TrimSpace(requestID)}
}

package relayagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

type decodeResponse[T any] func([]byte, time.Time) (T, error)

func call[T interface{ Validate(time.Time) error }, R any](
	ctx context.Context,
	agent *Agent,
	method, path string,
	request T,
	decode decodeResponse[R],
	destination *R,
) error {
	now := agent.config.Now().UTC()
	raw, err := relaycontrol.Encode(request, now)
	if err != nil {
		return err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, agent.config.ControlPlaneURL+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("create Relay control request")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if agent.config.BearerTokenFile != "" {
		token, err := readBearerToken(agent.config.BearerTokenFile)
		if err != nil {
			return err
		}
		httpRequest.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := agent.config.HTTPClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("send Relay control request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseRaw, readErr := io.ReadAll(io.LimitReader(response.Body, relaycontrol.MaximumBodyBytes+1))
	if readErr != nil || len(responseRaw) > relaycontrol.MaximumBodyBytes {
		return errors.New("read Relay control response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var document struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(responseRaw, &document)
		return &HTTPError{Status: response.StatusCode, Code: document.Error.Code}
	}
	decoded, err := decode(responseRaw, agent.config.Now().UTC())
	if err != nil {
		return err
	}
	*destination = decoded
	return nil
}

type HTTPError struct {
	Status int
	Code   string
}

func (err *HTTPError) Error() string {
	return fmt.Sprintf("Relay control HTTP %d (%s)", err.Status, err.Code)
}

func (err *HTTPError) HTTPStatus() int { return err.Status }

func isLeaseError(err error) bool {
	var httpError *HTTPError
	return errors.As(err, &httpError) &&
		(httpError.Status == http.StatusNotFound || httpError.Status == http.StatusConflict)
}

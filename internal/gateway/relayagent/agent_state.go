package relayagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (agent *Agent) Ready() bool {
	if agent == nil {
		return false
	}
	agent.mu.RLock()
	defer agent.mu.RUnlock()
	return agent.lifecycle == lifecycleRunning && agent.lastError == nil && agent.relayID != "" &&
		agent.leaseExpiresAt.After(agent.config.Now().UTC())
}

func (agent *Agent) RelayID() string {
	agent.mu.RLock()
	defer agent.mu.RUnlock()
	return agent.relayID
}

// DoJSON calls an authenticated ControlPlane internal API over the same trusted
// transport used by Relay registration.
func (agent *Agent) DoJSON(ctx context.Context, method, path string, input, output any) error {
	if agent == nil || ctx == nil || !strings.HasPrefix(path, "/internal/") {
		return errors.New("relay internal request is invalid")
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) == 0 || len(raw) > trafficcontrol.MaximumBodyBytes {
		return errors.New("encode Relay internal request")
	}
	request, err := http.NewRequestWithContext(ctx, method, agent.config.ControlPlaneURL+path, bytes.NewReader(raw))
	if err != nil {
		return errors.New("create Relay internal request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if agent.config.BearerTokenFile != "" {
		token, tokenErr := readBearerToken(agent.config.BearerTokenFile)
		if tokenErr != nil {
			return tokenErr
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := agent.config.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("send Relay internal request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseRaw, readErr := io.ReadAll(io.LimitReader(response.Body, trafficcontrol.MaximumBodyBytes+1))
	if readErr != nil || len(responseRaw) > trafficcontrol.MaximumBodyBytes {
		return errors.New("read Relay internal response")
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
	if output == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errors.New("decode Relay internal response")
	}
	return nil
}

func (agent *Agent) Done() <-chan struct{} { return agent.done }

func (agent *Agent) Stop() {
	if agent == nil {
		return
	}
	agent.mu.RLock()
	cancel := agent.cancel
	agent.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (agent *Agent) Drain(ctx context.Context) error {
	if agent == nil {
		return nil
	}
	agent.config.Reporter.BeginDrain()
	return agent.heartbeat(ctx)
}

func (agent *Agent) setError(err error) {
	agent.mu.Lock()
	agent.lastError = err
	agent.mu.Unlock()
}

func (agent *Agent) logf(format string, values ...any) {
	if agent.config.Logger != nil {
		agent.config.Logger.Printf(format, values...)
	}
}

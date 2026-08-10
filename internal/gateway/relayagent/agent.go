package relayagent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

type RuntimeReporter interface {
	Snapshot() (relaycontrol.State, relaycontrol.Capacity)
	BeginDrain()
}

type ControlApplier interface {
	Apply(string, relaycontrol.VerificationKeySet, relaycontrol.RevocationSummary) error
	AppliedGenerations() (uint64, uint64)
}

type Config struct {
	ControllerURL   string
	Endpoint        string
	BearerTokenFile string
	HTTPClient      *http.Client
	Reporter        RuntimeReporter
	Applier         ControlApplier
	Now             func() time.Time
	Logger          *log.Logger
}

type Agent struct {
	config Config

	mu             sync.RWMutex
	relayID        string
	leaseID        string
	leaseExpiresAt time.Time
	heartbeatAfter time.Duration
	lastError      error
	started        bool
	cancel         context.CancelFunc
	done           chan struct{}
}

func New(config Config) (*Agent, error) {
	config.ControllerURL = strings.TrimRight(strings.TrimSpace(config.ControllerURL), "/")
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	controllerURL, err := url.Parse(config.ControllerURL)
	if err != nil || controllerURL.Scheme != "https" || controllerURL.Host == "" ||
		controllerURL.User != nil || controllerURL.RawQuery != "" || controllerURL.Fragment != "" ||
		(controllerURL.Path != "" && controllerURL.Path != "/") {
		return nil, errors.New("Relay Registry Controller URL must be an HTTPS origin")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "wss" || endpoint.Host == "" || endpoint.Path == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("Data Plane advertised endpoint must be an absolute WSS URL")
	}
	if config.HTTPClient == nil || config.Reporter == nil || config.Applier == nil {
		return nil, errors.New("Relay Agent HTTP client, runtime reporter, and control applier are required")
	}
	config.BearerTokenFile = strings.TrimSpace(config.BearerTokenFile)
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Agent{config: config, done: make(chan struct{})}, nil
}

func (agent *Agent) Start(ctx context.Context) error {
	if agent == nil || ctx == nil {
		return errors.New("Relay Agent context is required")
	}
	agent.mu.Lock()
	if agent.started {
		agent.mu.Unlock()
		return errors.New("Relay Agent is already started")
	}
	agent.mu.Unlock()
	if err := agent.register(ctx); err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	agent.mu.Lock()
	agent.started = true
	agent.cancel = cancel
	agent.mu.Unlock()
	go agent.run(runContext)
	return nil
}

func (agent *Agent) run(ctx context.Context) {
	defer close(agent.done)
	for {
		agent.mu.RLock()
		after := agent.heartbeatAfter
		agent.mu.RUnlock()
		timer := time.NewTimer(after)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := agent.heartbeat(ctx); err != nil {
			agent.setError(err)
			agent.log("Relay heartbeat failed: %v", err)
			if isLeaseError(err) {
				if registerErr := agent.register(ctx); registerErr == nil {
					continue
				} else {
					agent.setError(registerErr)
					agent.log("Relay re-registration failed: %v", registerErr)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (agent *Agent) register(ctx context.Context) error {
	state, capacity := agent.config.Reporter.Snapshot()
	keyGeneration, revocationGeneration := agent.config.Applier.AppliedGenerations()
	request := relaycontrol.NewRegistrationRequest()
	request.Endpoint = agent.config.Endpoint
	request.State = state
	request.Capacity = capacity
	request.AppliedKeyGeneration = keyGeneration
	request.AppliedRevocationGeneration = revocationGeneration
	var response relaycontrol.RegistrationResponse
	if err := call(agent, ctx, http.MethodPost, "/internal/v1/relays/register", request, relaycontrol.DecodeRegistrationResponse, &response); err != nil {
		return err
	}
	if err := agent.config.Applier.Apply(response.RelayID, response.Keys, response.Revocations); err != nil {
		return fmt.Errorf("apply Relay registration control state: %w", err)
	}
	if response.DesiredState == relaycontrol.StateDraining {
		agent.config.Reporter.BeginDrain()
	}
	agent.mu.Lock()
	agent.relayID = response.RelayID
	agent.leaseID = response.LeaseID
	agent.leaseExpiresAt = response.LeaseExpiresAt
	agent.heartbeatAfter = response.HeartbeatAfter
	agent.lastError = nil
	agent.mu.Unlock()
	// Acknowledge the just-applied generations immediately. Registration
	// reports the previous generations and is intentionally not allocatable yet.
	return agent.heartbeat(ctx)
}

func (agent *Agent) heartbeat(ctx context.Context) error {
	agent.mu.RLock()
	leaseID := agent.leaseID
	agent.mu.RUnlock()
	if leaseID == "" {
		return errors.New("Relay Agent has no lease")
	}
	state, capacity := agent.config.Reporter.Snapshot()
	keyGeneration, revocationGeneration := agent.config.Applier.AppliedGenerations()
	request := relaycontrol.NewHeartbeatRequest()
	request.LeaseID = leaseID
	request.State = state
	request.Capacity = capacity
	request.AppliedKeyGeneration = keyGeneration
	request.AppliedRevocationGeneration = revocationGeneration
	var response relaycontrol.HeartbeatResponse
	if err := call(agent, ctx, http.MethodPut, "/internal/v1/relays/heartbeat", request, relaycontrol.DecodeHeartbeatResponse, &response); err != nil {
		return err
	}
	agent.mu.RLock()
	relayID := agent.relayID
	agent.mu.RUnlock()
	if err := agent.config.Applier.Apply(relayID, response.Keys, response.Revocations); err != nil {
		return fmt.Errorf("apply Relay heartbeat control state: %w", err)
	}
	if response.DesiredState == relaycontrol.StateDraining {
		agent.config.Reporter.BeginDrain()
	}
	agent.mu.Lock()
	agent.leaseExpiresAt = response.LeaseExpiresAt
	agent.heartbeatAfter = response.HeartbeatAfter
	agent.lastError = nil
	agent.mu.Unlock()
	return nil
}

type decodeResponse[T any] func([]byte, time.Time) (T, error)

func call[T interface{ Validate(time.Time) error }, R any](
	agent *Agent,
	ctx context.Context,
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
	httpRequest, err := http.NewRequestWithContext(ctx, method, agent.config.ControllerURL+path, bytes.NewReader(raw))
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
	defer response.Body.Close()
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

func isLeaseError(err error) bool {
	var httpError *HTTPError
	return errors.As(err, &httpError) && (httpError.Status == http.StatusNotFound || httpError.Status == http.StatusConflict)
}

func (agent *Agent) Ready() bool {
	if agent == nil {
		return false
	}
	agent.mu.RLock()
	defer agent.mu.RUnlock()
	return agent.started && agent.lastError == nil && agent.relayID != "" &&
		agent.leaseExpiresAt.After(agent.config.Now().UTC())
}

func (agent *Agent) RelayID() string {
	agent.mu.RLock()
	defer agent.mu.RUnlock()
	return agent.relayID
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

func (agent *Agent) log(format string, values ...any) {
	if agent.config.Logger != nil {
		agent.config.Logger.Printf(format, values...)
	}
}

type ClientTLSConfig struct {
	CertificateFile string
	PrivateKeyFile  string
	ServerCAFile    string
	ServerName      string
}

func NewHTTPClient(config ClientTLSConfig) (*http.Client, error) {
	certificateFile := strings.TrimSpace(config.CertificateFile)
	privateKeyFile := strings.TrimSpace(config.PrivateKeyFile)
	if (certificateFile == "") != (privateKeyFile == "") {
		return nil, errors.New("Relay Agent client certificate and private key must be configured together")
	}
	var certificates []tls.Certificate
	if certificateFile != "" {
		certificate, err := tls.LoadX509KeyPair(certificateFile, privateKeyFile)
		if err != nil {
			return nil, errors.New("load Relay Agent client certificate")
		}
		certificates = []tls.Certificate{certificate}
	}
	caPEM, err := os.ReadFile(strings.TrimSpace(config.ServerCAFile))
	if err != nil {
		return nil, errors.New("read Relay Agent server CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("parse Relay Agent server CA")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: certificates,
		RootCAs: roots, ServerName: strings.TrimSpace(config.ServerName),
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}, nil
}

func readBearerToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("read Relay Agent bearer token")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	token := strings.TrimSpace(string(raw))
	if err != nil || len(raw) == 0 || len(raw) > 16<<10 || token == "" || strings.ContainsAny(token, "\r\n \t") {
		return "", errors.New("Relay Agent bearer token is invalid")
	}
	return token, nil
}

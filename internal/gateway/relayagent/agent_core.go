package relayagent

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
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
	Apply(string, string, relaycontrol.VerificationKeySet, relaycontrol.RevocationSummary) error
	AppliedGenerations() (uint64, uint64)
}

type Config struct {
	ControlPlaneURL        string
	Endpoint               string
	BearerTokenFile        string
	HTTPClient             *http.Client
	Reporter               RuntimeReporter
	Applier                ControlApplier
	Now                    func() time.Time
	Logger                 *log.Logger
	RegistrationAttempts   int
	RegistrationRetryDelay time.Duration
	TrafficEncryption      bool
	NoisePublicKey         string
}

const (
	defaultRegistrationAttempts   = 10
	defaultRegistrationRetryDelay = 500 * time.Millisecond
)

type lifecycleState uint8

const (
	lifecycleIdle lifecycleState = iota
	lifecycleStarting
	lifecycleRunning
	lifecycleStopped
)

type Agent struct {
	config Config

	mu              sync.RWMutex
	relayID         string
	ticketIssuer    string
	leaseID         string
	leaseExpiresAt  time.Time
	heartbeatAfter  time.Duration
	selectedVersion string
	lastError       error
	lifecycle       lifecycleState
	cancel          context.CancelFunc
	done            chan struct{}
}

func New(config Config) (*Agent, error) {
	config.ControlPlaneURL = strings.TrimRight(strings.TrimSpace(config.ControlPlaneURL), "/")
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	controlPlaneURL, err := url.Parse(config.ControlPlaneURL)
	if err != nil || controlPlaneURL.Scheme != "https" || controlPlaneURL.Host == "" ||
		controlPlaneURL.User != nil || controlPlaneURL.RawQuery != "" || controlPlaneURL.Fragment != "" ||
		(controlPlaneURL.Path != "" && controlPlaneURL.Path != "/") {
		return nil, errors.New("relay registry ControlPlane URL must be an HTTPS origin")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil {
		return nil, errors.New("data plane advertised endpoint must be an absolute WS or WSS URL")
	}
	invalidEndpoint := (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") ||
		endpoint.Host == "" || endpoint.Path == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != ""
	if invalidEndpoint {
		return nil, errors.New("data plane advertised endpoint must be an absolute WS or WSS URL")
	}
	if config.HTTPClient == nil || config.Reporter == nil || config.Applier == nil {
		return nil, errors.New("relay agent HTTP client, runtime reporter, and control applier are required")
	}
	config.BearerTokenFile = strings.TrimSpace(config.BearerTokenFile)
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RegistrationAttempts == 0 {
		config.RegistrationAttempts = defaultRegistrationAttempts
	}
	if config.RegistrationRetryDelay == 0 {
		config.RegistrationRetryDelay = defaultRegistrationRetryDelay
	}
	if config.RegistrationAttempts < 1 || config.RegistrationAttempts > 100 ||
		config.RegistrationRetryDelay < 10*time.Millisecond || config.RegistrationRetryDelay > 30*time.Second {
		return nil, errors.New("relay agent registration retry policy is invalid")
	}
	if config.TrafficEncryption {
		probe := relaycontrol.NewHeartbeatRequestForVersion(relaycontrol.APIVersionV2)
		probe.LeaseID = "00000000-0000-0000-0000-000000000000"
		probe.State = relaycontrol.StateReady
		probe.Capacity = relaycontrol.Capacity{
			MaximumPhysicalConnections: 1, MaximumLogicalStreams: 1,
		}
		probe.TrafficEncryption = new(true)
		probe.NoisePublicKey = config.NoisePublicKey
		if err := probe.Validate(time.Now()); err != nil {
			return nil, errors.New("relay agent Noise public key is invalid")
		}
	} else if config.NoisePublicKey != "" {
		return nil, errors.New("relay agent Noise public key requires traffic encryption")
	}
	return &Agent{config: config, done: make(chan struct{})}, nil
}

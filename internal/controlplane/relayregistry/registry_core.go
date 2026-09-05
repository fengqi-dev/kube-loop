package relayregistry

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

var (
	ErrNotFound                 = errors.New("relay lease not found")
	ErrConflict                 = errors.New("relay lease conflict")
	ErrUnavailable              = errors.New("no ready Data Plane is available")
	ErrAssignedRelayUnavailable = errors.New(
		"assigned Data Plane is unavailable",
	)
)

type EndpointPolicy func(relaycontrol.PeerIdentity, string) error

type Config struct {
	Now               func() time.Time
	TicketIssuer      string
	LeaseDuration     time.Duration
	HeartbeatAfter    time.Duration
	SupportedVersions []string
	VerificationKeys  relaycontrol.VerificationKeySet
	EndpointPolicy    EndpointPolicy
}

type Registry struct {
	mu          sync.Mutex
	config      Config
	relays      map[string]*relayRecord
	assignments map[string]assignmentRecord
}

type relayRecord struct {
	identity             relaycontrol.PeerIdentity
	relayID              string
	leaseID              string
	endpoint             string
	state                relaycontrol.State
	capacity             relaycontrol.Capacity
	appliedKeyGeneration uint64
	leaseExpiresAt       time.Time
	lastHeartbeatAt      time.Time
	reservations         uint32
	selectedVersion      string
	trafficEncryption    bool
	noisePublicKey       string
}

type assignmentRecord struct {
	response        relaycontrol.AllocationResponse
	generation      uint64
	networkSpecHash string
}

type RelayStatus struct {
	RelayID              string
	Endpoint             string
	State                relaycontrol.State
	Capacity             relaycontrol.Capacity
	AppliedKeyGeneration uint64
	LeaseExpiresAt       time.Time
	LastHeartbeatAt      time.Time
	Reservations         uint32
	Online               bool
	Topology             map[string]string
}

func New(config Config) (*Registry, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	config.TicketIssuer = strings.TrimRight(
		strings.TrimSpace(config.TicketIssuer),
		"/",
	)
	issuer, err := url.Parse(config.TicketIssuer)
	if err != nil || (issuer.Scheme != "http" && issuer.Scheme != "https") ||
		issuer.Host == "" ||
		issuer.User != nil ||
		issuer.RawQuery != "" ||
		issuer.Fragment != "" {
		return nil, errors.New(
			"relay registry Ticket issuer must be an absolute HTTP or HTTPS URL",
		)
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 45 * time.Second
	}
	if config.HeartbeatAfter == 0 {
		config.HeartbeatAfter = 10 * time.Second
	}
	if config.LeaseDuration < 5*time.Second ||
		config.LeaseDuration > 5*time.Minute ||
		config.HeartbeatAfter < time.Second ||
		config.HeartbeatAfter > time.Minute ||
		config.HeartbeatAfter >= config.LeaseDuration {
		return nil, errors.New("relay registry lease configuration is invalid")
	}
	if len(config.SupportedVersions) == 0 {
		config.SupportedVersions = []string{
			relaycontrol.APIVersionV2,
			relaycontrol.APIVersionV1,
		}
	}
	if _, err := relaycontrol.NegotiateVersion(
		config.SupportedVersions,
		[]string{relaycontrol.APIVersionV2, relaycontrol.APIVersionV1},
	); err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	if err := config.VerificationKeys.Validate(now); err != nil {
		return nil, err
	}
	config.SupportedVersions = append(
		[]string(nil),
		config.SupportedVersions...)
	config.VerificationKeys = cloneKeys(config.VerificationKeys)
	return &Registry{
		config: config, relays: make(map[string]*relayRecord),
		assignments: make(
			map[string]assignmentRecord,
		),
	}, nil
}

package relayregistry

import (
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/google/uuid"
)

var (
	ErrNotFound                 = errors.New("Relay lease not found")
	ErrConflict                 = errors.New("Relay lease conflict")
	ErrUnavailable              = errors.New("no ready Data Plane is available")
	ErrAssignedRelayUnavailable = errors.New("assigned Data Plane is unavailable")
)

type EndpointPolicy func(relaycontrol.PeerIdentity, string) error

type Config struct {
	Now               func() time.Time
	TicketIssuer      string
	LeaseDuration     time.Duration
	HeartbeatAfter    time.Duration
	SupportedVersions []string
	VerificationKeys  relaycontrol.VerificationKeySet
	Revocations       relaycontrol.RevocationSummary
	EndpointPolicy    EndpointPolicy
}

type Registry struct {
	mu              sync.Mutex
	config          Config
	relays          map[string]*relayRecord
	assignments     map[string]assignmentRecord
	restoredDesired map[string]relaycontrol.State
}

type relayRecord struct {
	identity                    relaycontrol.PeerIdentity
	relayID                     string
	leaseID                     string
	endpoint                    string
	state                       relaycontrol.State
	desiredState                relaycontrol.State
	capacity                    relaycontrol.Capacity
	appliedKeyGeneration        uint64
	appliedRevocationGeneration uint64
	leaseExpiresAt              time.Time
	lastHeartbeatAt             time.Time
	reservations                uint32
}

type assignmentRecord struct {
	response        relaycontrol.AllocationResponse
	generation      uint64
	networkSpecHash string
}

type RelayStatus struct {
	RelayID                     string
	Endpoint                    string
	State                       relaycontrol.State
	DesiredState                relaycontrol.State
	Capacity                    relaycontrol.Capacity
	AppliedKeyGeneration        uint64
	AppliedRevocationGeneration uint64
	LeaseExpiresAt              time.Time
	LastHeartbeatAt             time.Time
	Reservations                uint32
	Online                      bool
	Topology                    map[string]string
}

func New(config Config) (*Registry, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	config.TicketIssuer = strings.TrimRight(strings.TrimSpace(config.TicketIssuer), "/")
	issuer, err := url.Parse(config.TicketIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" {
		return nil, errors.New("Relay Registry Ticket issuer must be an absolute HTTPS URL")
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 45 * time.Second
	}
	if config.HeartbeatAfter == 0 {
		config.HeartbeatAfter = 10 * time.Second
	}
	if config.LeaseDuration < 5*time.Second || config.LeaseDuration > 5*time.Minute ||
		config.HeartbeatAfter < time.Second || config.HeartbeatAfter > time.Minute ||
		config.HeartbeatAfter >= config.LeaseDuration {
		return nil, errors.New("Relay Registry lease configuration is invalid")
	}
	if len(config.SupportedVersions) == 0 {
		config.SupportedVersions = []string{relaycontrol.APIVersion}
	}
	if _, err := relaycontrol.NegotiateVersion(config.SupportedVersions, []string{relaycontrol.APIVersion}); err != nil {
		return nil, err
	}
	now := config.Now().UTC()
	if err := config.VerificationKeys.Validate(now); err != nil {
		return nil, err
	}
	if err := config.Revocations.Validate(now); err != nil {
		return nil, err
	}
	config.SupportedVersions = append([]string(nil), config.SupportedVersions...)
	config.VerificationKeys = cloneKeys(config.VerificationKeys)
	config.Revocations = cloneRevocations(config.Revocations)
	return &Registry{
		config: config, relays: make(map[string]*relayRecord),
		assignments: make(map[string]assignmentRecord), restoredDesired: make(map[string]relaycontrol.State),
	}, nil
}

func (registry *Registry) Register(
	identity relaycontrol.PeerIdentity,
	request relaycontrol.RegistrationRequest,
) (relaycontrol.RegistrationResponse, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := identity.Validate(); err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	if err := request.Validate(now); err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	selectedVersion, err := relaycontrol.NegotiateVersion(registry.config.SupportedVersions, request.SupportedVersions)
	if err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	if selectedVersion != relaycontrol.APIVersion {
		return relaycontrol.RegistrationResponse{}, errors.New("negotiated Relay protocol is not implemented")
	}
	if registry.config.EndpointPolicy != nil {
		if err := registry.config.EndpointPolicy(identity, request.Endpoint); err != nil {
			return relaycontrol.RegistrationResponse{}, err
		}
	}
	if request.AppliedKeyGeneration > registry.config.VerificationKeys.Generation ||
		request.AppliedRevocationGeneration > registry.config.Revocations.Generation {
		return relaycontrol.RegistrationResponse{}, errors.New("Relay reports an unknown control-plane generation")
	}
	relayID, err := identity.RelayID()
	if err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	leaseID := uuid.NewString()
	desired := relaycontrol.StateReady
	if request.State == relaycontrol.StateDraining {
		desired = relaycontrol.StateDraining
	}
	if restored, ok := registry.restoredDesired[relayID]; ok {
		desired = restored
	}
	reservations := registry.assignmentCountLocked(relayID)
	registry.relays[relayID] = &relayRecord{
		identity: cloneIdentity(identity), relayID: relayID, leaseID: leaseID,
		endpoint: request.Endpoint, state: request.State, desiredState: desired,
		capacity: request.Capacity, appliedKeyGeneration: request.AppliedKeyGeneration,
		appliedRevocationGeneration: request.AppliedRevocationGeneration,
		leaseExpiresAt:              now.Add(registry.config.LeaseDuration), lastHeartbeatAt: now,
		reservations: reservations,
	}
	return registry.registrationResponseLocked(relayID, selectedVersion), nil
}

func (registry *Registry) Heartbeat(
	identity relaycontrol.PeerIdentity,
	request relaycontrol.HeartbeatRequest,
) (relaycontrol.HeartbeatResponse, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := identity.Validate(); err != nil {
		return relaycontrol.HeartbeatResponse{}, err
	}
	if err := request.Validate(now); err != nil {
		return relaycontrol.HeartbeatResponse{}, err
	}
	relayID, err := identity.RelayID()
	if err != nil {
		return relaycontrol.HeartbeatResponse{}, err
	}
	relay := registry.relays[relayID]
	if relay == nil {
		return relaycontrol.HeartbeatResponse{}, ErrNotFound
	}
	if relay.leaseID != request.LeaseID {
		return relaycontrol.HeartbeatResponse{}, ErrConflict
	}
	if !relay.leaseExpiresAt.After(now) {
		return relaycontrol.HeartbeatResponse{}, ErrNotFound
	}
	if request.AppliedKeyGeneration > registry.config.VerificationKeys.Generation ||
		request.AppliedRevocationGeneration > registry.config.Revocations.Generation {
		return relaycontrol.HeartbeatResponse{}, errors.New("Relay reports an unknown control-plane generation")
	}
	relay.state = request.State
	relay.capacity = request.Capacity
	relay.appliedKeyGeneration = request.AppliedKeyGeneration
	relay.appliedRevocationGeneration = request.AppliedRevocationGeneration
	relay.lastHeartbeatAt = now
	relay.leaseExpiresAt = now.Add(registry.config.LeaseDuration)
	return relaycontrol.HeartbeatResponse{
		Envelope:       relaycontrol.NewHeartbeatResponse().Envelope,
		LeaseExpiresAt: relay.leaseExpiresAt, HeartbeatAfter: registry.config.HeartbeatAfter,
		DesiredState: relay.desiredState, Keys: cloneKeys(registry.config.VerificationKeys),
		Revocations: cloneRevocations(registry.config.Revocations),
	}, nil
}

func (registry *Registry) Allocate(request relaycontrol.AllocationRequest) (relaycontrol.AllocationResponse, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := request.Validate(now); err != nil {
		return relaycontrol.AllocationResponse{}, err
	}
	var displaced assignmentRecord
	var displacedRelay *relayRecord
	reservationReleased := false
	reassigning := false
	if existing, ok := registry.assignments[request.SessionID]; ok {
		if request.NetworkSpecHash != existing.networkSpecHash || request.Generation < existing.generation {
			return relaycontrol.AllocationResponse{}, ErrConflict
		}
		relay := registry.relays[existing.response.RelayID]
		if relay == nil || relay.leaseID != existing.response.LeaseID || !registry.availableLocked(relay, now) {
			// A newer authoritative Session generation is the fencing boundary that
			// permits failover. Reusing the same generation would allow two Relays
			// to accept tickets for one Session concurrently during a partition.
			if request.Generation == existing.generation {
				return relaycontrol.AllocationResponse{}, ErrAssignedRelayUnavailable
			}
			displaced = existing
			displacedRelay = relay
			reassigning = true
			delete(registry.assignments, request.SessionID)
			if relay != nil && relay.reservations > 0 {
				relay.reservations--
				reservationReleased = true
			}
		} else {
			existing.generation = request.Generation
			registry.assignments[request.SessionID] = existing
			return existing.response, nil
		}
	}
	candidates := make([]*relayRecord, 0, len(registry.relays))
	for _, relay := range registry.relays {
		if registry.availableLocked(relay, now) {
			candidates = append(candidates, relay)
		}
	}
	if len(candidates) == 0 {
		if reassigning {
			registry.assignments[request.SessionID] = displaced
			if reservationReleased {
				displacedRelay.reservations++
			}
			return relaycontrol.AllocationResponse{}, ErrAssignedRelayUnavailable
		}
		return relaycontrol.AllocationResponse{}, ErrUnavailable
	}
	slices.SortFunc(candidates, func(left, right *relayRecord) int {
		if comparison := compareTopology(left.identity.Topology, right.identity.Topology, request.Topology); comparison != 0 {
			return comparison
		}
		if comparison := compareRatio(
			left.capacity.ActiveLogicalStreams+left.reservations, left.capacity.MaximumLogicalStreams,
			right.capacity.ActiveLogicalStreams+right.reservations, right.capacity.MaximumLogicalStreams,
		); comparison != 0 {
			return comparison
		}
		if comparison := compareRatio(
			left.capacity.ActivePhysicalConnections, left.capacity.MaximumPhysicalConnections,
			right.capacity.ActivePhysicalConnections, right.capacity.MaximumPhysicalConnections,
		); comparison != 0 {
			return comparison
		}
		if left.relayID < right.relayID {
			return -1
		}
		if left.relayID > right.relayID {
			return 1
		}
		return 0
	})
	selected := candidates[0]
	selected.reservations++
	assignment := relaycontrol.AllocationResponse{
		Envelope: relaycontrol.NewAllocationResponse().Envelope,
		RelayID:  selected.relayID, LeaseID: selected.leaseID,
		Endpoint: selected.endpoint, AssignedAt: now,
	}
	registry.assignments[request.SessionID] = assignmentRecord{
		response: assignment, generation: request.Generation, networkSpecHash: request.NetworkSpecHash,
	}
	return assignment, nil
}

func (registry *Registry) Release(sessionID string, generation uint64) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	assignment, exists := registry.assignments[sessionID]
	if !exists || assignment.generation != generation {
		return false
	}
	delete(registry.assignments, sessionID)
	if relay := registry.relays[assignment.response.RelayID]; relay != nil && relay.reservations > 0 {
		relay.reservations--
	}
	return true
}

func (registry *Registry) SetDesiredState(relayID string, state relaycontrol.State) error {
	if state != relaycontrol.StateReady && state != relaycontrol.StateDraining {
		return errors.New("Relay desired state is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	relay := registry.relays[relayID]
	if relay == nil {
		return ErrNotFound
	}
	relay.desiredState = state
	registry.restoredDesired[relayID] = state
	return nil
}

// RestoreDesiredState installs durable control-plane intent even while a Relay
// is offline. The next registration observes it before becoming allocatable.
func (registry *Registry) RestoreDesiredState(relayID string, state relaycontrol.State) error {
	if strings.TrimSpace(relayID) == "" || len(relayID) > 256 ||
		(state != relaycontrol.StateReady && state != relaycontrol.StateDraining) {
		return errors.New("Relay durable desired state is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.restoredDesired[relayID] = state
	if relay := registry.relays[relayID]; relay != nil {
		relay.desiredState = state
	}
	return nil
}

func (registry *Registry) UpdateControlPlaneState(
	keys relaycontrol.VerificationKeySet,
	revocations relaycontrol.RevocationSummary,
) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := keys.Validate(now); err != nil {
		return err
	}
	if err := revocations.Validate(now); err != nil {
		return err
	}
	if keys.Generation < registry.config.VerificationKeys.Generation ||
		revocations.Generation < registry.config.Revocations.Generation {
		return errors.New("Relay control-plane generation cannot move backwards")
	}
	registry.config.VerificationKeys = cloneKeys(keys)
	registry.config.Revocations = cloneRevocations(revocations)
	return nil
}

func (registry *Registry) Snapshot() []RelayStatus {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	result := make([]RelayStatus, 0, len(registry.relays))
	for _, relay := range registry.relays {
		result = append(result, RelayStatus{
			RelayID: relay.relayID, Endpoint: relay.endpoint, State: relay.state,
			DesiredState: relay.desiredState, Capacity: relay.capacity,
			AppliedKeyGeneration:        relay.appliedKeyGeneration,
			AppliedRevocationGeneration: relay.appliedRevocationGeneration,
			LeaseExpiresAt:              relay.leaseExpiresAt, LastHeartbeatAt: relay.lastHeartbeatAt,
			Reservations: relay.reservations, Online: relay.leaseExpiresAt.After(now),
			Topology: cloneMap(relay.identity.Topology),
		})
	}
	slices.SortFunc(result, func(left, right RelayStatus) int {
		if left.RelayID < right.RelayID {
			return -1
		}
		if left.RelayID > right.RelayID {
			return 1
		}
		return 0
	})
	return result
}

func (registry *Registry) registrationResponseLocked(relayID, selectedVersion string) relaycontrol.RegistrationResponse {
	relay := registry.relays[relayID]
	return relaycontrol.RegistrationResponse{
		Envelope: relaycontrol.NewRegistrationResponse().Envelope, SelectedVersion: selectedVersion,
		TicketIssuer: registry.config.TicketIssuer,
		RelayID:      relay.relayID, LeaseID: relay.leaseID, LeaseExpiresAt: relay.leaseExpiresAt,
		HeartbeatAfter: registry.config.HeartbeatAfter, DesiredState: relay.desiredState,
		Keys:        cloneKeys(registry.config.VerificationKeys),
		Revocations: cloneRevocations(registry.config.Revocations),
	}
}

func (registry *Registry) availableLocked(relay *relayRecord, now time.Time) bool {
	if relay.state != relaycontrol.StateReady || relay.desiredState != relaycontrol.StateReady ||
		!relay.leaseExpiresAt.After(now) ||
		relay.appliedKeyGeneration < registry.config.VerificationKeys.Generation ||
		relay.appliedRevocationGeneration < registry.config.Revocations.Generation {
		return false
	}
	logical := uint64(relay.capacity.ActiveLogicalStreams) + uint64(relay.reservations)
	return logical < uint64(relay.capacity.MaximumLogicalStreams) &&
		relay.capacity.ActivePhysicalConnections < relay.capacity.MaximumPhysicalConnections
}

func (registry *Registry) assignmentCountLocked(relayID string) uint32 {
	var count uint32
	for _, assignment := range registry.assignments {
		if assignment.response.RelayID == relayID {
			count++
		}
	}
	return count
}

func compareTopology(left, right, wanted map[string]string) int {
	leftMatches, rightMatches := 0, 0
	for key, value := range wanted {
		if left[key] == value {
			leftMatches++
		}
		if right[key] == value {
			rightMatches++
		}
	}
	if leftMatches > rightMatches {
		return -1
	}
	if leftMatches < rightMatches {
		return 1
	}
	return 0
}

func compareRatio(leftValue, leftMaximum, rightValue, rightMaximum uint32) int {
	left := uint64(leftValue) * uint64(rightMaximum)
	right := uint64(rightValue) * uint64(leftMaximum)
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func cloneIdentity(identity relaycontrol.PeerIdentity) relaycontrol.PeerIdentity {
	identity.Topology = cloneMap(identity.Topology)
	return identity
}

func cloneKeys(keys relaycontrol.VerificationKeySet) relaycontrol.VerificationKeySet {
	copyKeys := keys
	copyKeys.Keys = append([]relaycontrol.VerificationKey(nil), keys.Keys...)
	return copyKeys
}

func cloneRevocations(summary relaycontrol.RevocationSummary) relaycontrol.RevocationSummary {
	copySummary := summary
	copySummary.Sessions = append([]relaycontrol.RevokedSession(nil), summary.Sessions...)
	return copySummary
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	maps.Copy(result, source)
	return result
}

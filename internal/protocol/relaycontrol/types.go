package relaycontrol

import "time"

const (
	APIVersion       = "relay.kubeloop.io/v1"
	MaximumBodyBytes = 64 << 10

	KindRegistrationRequest  = "RelayRegistration"
	KindRegistrationResponse = "RelayRegistrationResult"
	KindHeartbeatRequest     = "RelayHeartbeat"
	KindHeartbeatResponse    = "RelayHeartbeatResult"
	KindAllocationRequest    = "SessionAllocation"
	KindAllocationResponse   = "SessionAssignment"
)

type State string

const (
	StateReady    State = "ready"
	StateDraining State = "draining"
)

type Envelope struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

type Capacity struct {
	MaximumPhysicalConnections uint32 `json:"maximumPhysicalConnections"`
	MaximumLogicalStreams      uint32 `json:"maximumLogicalStreams"`
	ActivePhysicalConnections  uint32 `json:"activePhysicalConnections"`
	ActiveLogicalStreams       uint32 `json:"activeLogicalStreams"`
}

type VerificationKey struct {
	ID        string    `json:"id"`
	Algorithm string    `json:"algorithm"`
	PublicKey string    `json:"publicKeyPem"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
}

type VerificationKeySet struct {
	Generation uint64            `json:"generation"`
	Keys       []VerificationKey `json:"keys"`
}

type RevocationSummary struct {
	Generation  uint64           `json:"generation"`
	SHA256      string           `json:"sha256,omitempty"`
	GeneratedAt time.Time        `json:"generatedAt"`
	ValidUntil  time.Time        `json:"validUntil"`
	Sessions    []RevokedSession `json:"sessions,omitempty"`
}

type RevokedSession struct {
	SessionSHA256     string    `json:"sessionSha256"`
	MaximumGeneration uint64    `json:"maximumGeneration"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

// RegistrationRequest deliberately contains no Relay ID. The Control Plane
// derives it from the authenticated transport identity, never from this body.
type RegistrationRequest struct {
	Envelope

	SupportedVersions           []string `json:"supportedVersions"`
	Endpoint                    string   `json:"endpoint"`
	State                       State    `json:"state"`
	Capacity                    Capacity `json:"capacity"`
	AppliedKeyGeneration        uint64   `json:"appliedKeyGeneration"`
	AppliedRevocationGeneration uint64   `json:"appliedRevocationGeneration"`
}

type RegistrationResponse struct {
	Envelope

	SelectedVersion string             `json:"selectedVersion"`
	TicketIssuer    string             `json:"ticketIssuer"`
	RelayID         string             `json:"relayId"`
	LeaseID         string             `json:"leaseId"`
	LeaseExpiresAt  time.Time          `json:"leaseExpiresAt"`
	HeartbeatAfter  time.Duration      `json:"heartbeatAfter"`
	DesiredState    State              `json:"desiredState"`
	Keys            VerificationKeySet `json:"verificationKeys"`
	Revocations     RevocationSummary  `json:"revocations"`
}

type HeartbeatRequest struct {
	Envelope

	LeaseID                     string   `json:"leaseId"`
	State                       State    `json:"state"`
	Capacity                    Capacity `json:"capacity"`
	AppliedKeyGeneration        uint64   `json:"appliedKeyGeneration"`
	AppliedRevocationGeneration uint64   `json:"appliedRevocationGeneration"`
}

type HeartbeatResponse struct {
	Envelope

	LeaseExpiresAt time.Time          `json:"leaseExpiresAt"`
	HeartbeatAfter time.Duration      `json:"heartbeatAfter"`
	DesiredState   State              `json:"desiredState"`
	Keys           VerificationKeySet `json:"verificationKeys"`
	Revocations    RevocationSummary  `json:"revocations"`
}

type AllocationRequest struct {
	Envelope

	SessionID       string            `json:"sessionId"`
	Generation      uint64            `json:"generation"`
	NetworkSpecHash string            `json:"networkSpecHash"`
	Topology        map[string]string `json:"topology,omitempty"`
}

type AllocationResponse struct {
	Envelope

	RelayID    string    `json:"relayId"`
	LeaseID    string    `json:"leaseId"`
	Endpoint   string    `json:"endpoint"`
	AssignedAt time.Time `json:"assignedAt"`
}

func NewRegistrationRequest() RegistrationRequest {
	return RegistrationRequest{
		Envelope:          Envelope{APIVersion: APIVersion, Kind: KindRegistrationRequest},
		SupportedVersions: []string{APIVersion},
	}
}

func NewRegistrationResponse() RegistrationResponse {
	return RegistrationResponse{
		Envelope:        Envelope{APIVersion: APIVersion, Kind: KindRegistrationResponse},
		SelectedVersion: APIVersion,
	}
}

func NewHeartbeatRequest() HeartbeatRequest {
	return HeartbeatRequest{Envelope: Envelope{APIVersion: APIVersion, Kind: KindHeartbeatRequest}}
}

func NewHeartbeatResponse() HeartbeatResponse {
	return HeartbeatResponse{Envelope: Envelope{APIVersion: APIVersion, Kind: KindHeartbeatResponse}}
}

func NewAllocationRequest() AllocationRequest {
	return AllocationRequest{Envelope: Envelope{APIVersion: APIVersion, Kind: KindAllocationRequest}}
}

func NewAllocationResponse() AllocationResponse {
	return AllocationResponse{Envelope: Envelope{APIVersion: APIVersion, Kind: KindAllocationResponse}}
}

package relaycontrol

import "time"

const (
	APIVersionV1     = "relay.kubeloop.io/v1"
	APIVersionV2     = "relay.kubeloop.io/v2"
	APIVersion       = APIVersionV1
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

// RegistrationRequest deliberately contains no Relay ID. The Control Plane
// derives it from the authenticated transport identity, never from this body.
type RegistrationRequest struct {
	Envelope

	SupportedVersions []string `json:"supportedVersions"`
	Endpoint          string   `json:"endpoint"`
	State             State    `json:"state"`
	Capacity          Capacity `json:"capacity"`
}

type RegistrationResponse struct {
	Envelope

	SelectedVersion string             `json:"selectedVersion"`
	TicketIssuer    string             `json:"ticketIssuer"`
	RelayID         string             `json:"relayId"`
	LeaseID         string             `json:"leaseId"`
	LeaseExpiresAt  time.Time          `json:"leaseExpiresAt"`
	HeartbeatAfter  time.Duration      `json:"heartbeatAfter"`
	Keys            VerificationKeySet `json:"verificationKeys"`
}

type HeartbeatRequest struct {
	Envelope

	LeaseID              string   `json:"leaseId"`
	State                State    `json:"state"`
	Capacity             Capacity `json:"capacity"`
	AppliedKeyGeneration uint64   `json:"appliedKeyGeneration"`
	TrafficEncryption    *bool    `json:"trafficEncryption,omitempty"`
	NoisePublicKey       string   `json:"noisePublicKey,omitempty"`
}

type HeartbeatResponse struct {
	Envelope

	LeaseExpiresAt time.Time     `json:"leaseExpiresAt"`
	HeartbeatAfter time.Duration `json:"heartbeatAfter"`
}

type AllocationRequest struct {
	Envelope

	SessionID         string            `json:"sessionId"`
	Generation        uint64            `json:"generation"`
	NetworkSpecHash   string            `json:"networkSpecHash"`
	Topology          map[string]string `json:"topology,omitempty"`
	TrafficEncryption *bool             `json:"trafficEncryption,omitempty"`
}

type AllocationResponse struct {
	Envelope

	RelayID           string    `json:"relayId"`
	LeaseID           string    `json:"leaseId"`
	Endpoint          string    `json:"endpoint"`
	AssignedAt        time.Time `json:"assignedAt"`
	TrafficEncryption bool      `json:"trafficEncryption,omitempty"`
	NoisePublicKey    string    `json:"noisePublicKey,omitempty"`
}

func NewRegistrationRequest() RegistrationRequest {
	return RegistrationRequest{
		APIVersion: APIVersion, Kind: KindRegistrationRequest,
		SupportedVersions: []string{APIVersionV1},
	}
}

func NewRegistrationRequestWithNegotiation() RegistrationRequest {
	request := NewRegistrationRequest()
	request.SupportedVersions = []string{APIVersionV2, APIVersionV1}
	return request
}

func NewRegistrationResponse() RegistrationResponse {
	return RegistrationResponse{
		APIVersion: APIVersion, Kind: KindRegistrationResponse,
		SelectedVersion: APIVersion,
	}
}

func NewHeartbeatRequest() HeartbeatRequest {
	return HeartbeatRequest{APIVersion: APIVersion, Kind: KindHeartbeatRequest}
}

func NewHeartbeatRequestForVersion(version string) HeartbeatRequest {
	return HeartbeatRequest{APIVersion: version, Kind: KindHeartbeatRequest}
}

func NewHeartbeatResponse() HeartbeatResponse {
	return HeartbeatResponse{APIVersion: APIVersion, Kind: KindHeartbeatResponse}
}

func NewHeartbeatResponseForVersion(version string) HeartbeatResponse {
	return HeartbeatResponse{APIVersion: version, Kind: KindHeartbeatResponse}
}

func NewAllocationRequest() AllocationRequest {
	return AllocationRequest{APIVersion: APIVersion, Kind: KindAllocationRequest}
}

func NewAllocationResponse() AllocationResponse {
	return AllocationResponse{APIVersion: APIVersion, Kind: KindAllocationResponse}
}

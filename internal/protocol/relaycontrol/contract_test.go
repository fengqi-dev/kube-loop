package relaycontrol

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVersionedMessagesRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	identity := validPeerIdentity()
	relayID, err := identity.RelayID()
	if err != nil {
		t.Fatal(err)
	}
	keys := testKeySet(t, now)
	revocations, err := NewRevocationSummary(1, []RevokedSession{{
		SessionSHA256: HashSessionID(uuid.NewString()), MaximumGeneration: 2, ExpiresAt: now.Add(time.Minute),
	}}, now.Add(-time.Second), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	capacity := Capacity{
		MaximumPhysicalConnections: 100, MaximumLogicalStreams: 1000,
		ActivePhysicalConnections: 4, ActiveLogicalStreams: 20,
	}
	leaseID := uuid.NewString()
	messages := []struct {
		name   string
		value  validMessage
		decode func([]byte, time.Time) error
	}{
		{"registration request", RegistrationRequest{
			Envelope: NewRegistrationRequest().Envelope, SupportedVersions: []string{APIVersion},
			Endpoint: "wss://relay.example.test/tunnel",
			State:    StateReady, Capacity: capacity,
		}, func(raw []byte, now time.Time) error { _, err := DecodeRegistrationRequest(raw, now); return err }},
		{"registration response", RegistrationResponse{
			Envelope: NewRegistrationResponse().Envelope, SelectedVersion: APIVersion,
			TicketIssuer: "https://control-plane.example.test",
			RelayID:      relayID, LeaseID: leaseID,
			LeaseExpiresAt: now.Add(time.Minute), HeartbeatAfter: 10 * time.Second,
			DesiredState: StateReady, Keys: keys, Revocations: revocations,
		}, func(raw []byte, now time.Time) error { _, err := DecodeRegistrationResponse(raw, now); return err }},
		{"heartbeat request", HeartbeatRequest{
			Envelope: NewHeartbeatRequest().Envelope, LeaseID: leaseID,
			State: StateDraining, Capacity: capacity,
		}, func(raw []byte, now time.Time) error { _, err := DecodeHeartbeatRequest(raw, now); return err }},
		{"heartbeat response", HeartbeatResponse{
			Envelope: NewHeartbeatResponse().Envelope, LeaseExpiresAt: now.Add(time.Minute),
			HeartbeatAfter: 10 * time.Second, DesiredState: StateDraining, Keys: keys, Revocations: revocations,
		}, func(raw []byte, now time.Time) error { _, err := DecodeHeartbeatResponse(raw, now); return err }},
		{"allocation request", AllocationRequest{
			Envelope: NewAllocationRequest().Envelope, SessionID: uuid.NewString(), Generation: 1,
			NetworkSpecHash: hex.EncodeToString(make([]byte, sha256.Size)),
		}, func(raw []byte, now time.Time) error { _, err := DecodeAllocationRequest(raw, now); return err }},
		{"allocation response", AllocationResponse{
			Envelope: NewAllocationResponse().Envelope, RelayID: relayID, LeaseID: leaseID,
			Endpoint: "wss://relay.example.test/tunnel", AssignedAt: now,
		}, func(raw []byte, now time.Time) error { _, err := DecodeAllocationResponse(raw, now); return err }},
	}
	for _, test := range messages {
		t.Run(test.name, func(t *testing.T) {
			raw, err := Encode(test.value, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.decode(raw, now); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRelayEndpointAllowsWSAndWSS(t *testing.T) {
	for _, endpoint := range []string{"ws://relay.example.test/tunnel", "wss://relay.example.test/tunnel"} {
		if err := validateEndpoint(endpoint); err != nil {
			t.Fatalf("validateEndpoint(%q): %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://relay.example.test/tunnel", "ws://relay.example.test"} {
		if err := validateEndpoint(endpoint); err == nil {
			t.Fatalf("validateEndpoint(%q) succeeded", endpoint)
		}
	}
}

func TestTicketIssuerAllowsHTTPAndHTTPS(t *testing.T) {
	for _, issuer := range []string{"http://control-plane.example.test", "https://control-plane.example.test"} {
		if !validTicketIssuer(issuer) {
			t.Fatalf("validTicketIssuer(%q) = false", issuer)
		}
	}
	if validTicketIssuer("ftp://control-plane.example.test") {
		t.Fatal("FTP Ticket issuer was accepted")
	}
}

func TestRegistrationCannotSubmitRelayIdentityAndUnknownVersionsFail(t *testing.T) {
	now := time.Now().UTC()
	request := RegistrationRequest{
		Envelope: NewRegistrationRequest().Envelope, Endpoint: "wss://relay.example.test/tunnel",
		State: StateReady, Capacity: Capacity{MaximumPhysicalConnections: 1, MaximumLogicalStreams: 1},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	withRelayID := raw[:len(raw)-1]
	withRelayID = append(withRelayID, []byte(`,"relayId":"client-controlled"}`)...)
	if _, err := DecodeRegistrationRequest(withRelayID, now); err == nil {
		t.Fatal("registration accepted a client-supplied Relay ID")
	}
	withSecret := raw[:len(raw)-1]
	withSecret = append(withSecret, []byte(`,"refreshToken":"secret"}`)...)
	if _, err := DecodeRegistrationRequest(withSecret, now); err == nil {
		t.Fatal("registration accepted a forbidden credential field")
	}
	request.APIVersion = "relay.kubeloop.io/v2"
	raw, _ = json.Marshal(request)
	if _, err := DecodeRegistrationRequest(raw, now); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown version error = %v", err)
	}
	if _, err := DecodeRegistrationRequest(append(raw, []byte(` {}`)...), now); err == nil {
		t.Fatal("multiple relay control documents were accepted")
	}
	if _, err := DecodeRegistrationRequest(make([]byte, MaximumBodyBytes+1), now); err == nil {
		t.Fatal("oversized relay control body was accepted")
	}
}

func TestRollingUpgradeNegotiatesHighestCommonVersion(t *testing.T) {
	v1, v2 := APIVersion, "relay.kubeloop.io/v2"
	for _, test := range []struct {
		name  string
		local []string
		peer  []string
		want  string
	}{
		{"same version", []string{v1}, []string{v1}, v1},
		{"new Control Plane old Data Plane", []string{v2, v1}, []string{v1}, v1},
		{"old Control Plane new Data Plane", []string{v1}, []string{v2, v1}, v1},
		{"both new", []string{v2, v1}, []string{v1, v2}, v2},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NegotiateVersion(test.local, test.peer)
			if err != nil || got != test.want {
				t.Fatalf("version = %q err = %v, want %q", got, err, test.want)
			}
		})
	}
	if _, err := NegotiateVersion([]string{v1}, []string{v2}); err == nil {
		t.Fatal("incompatible rolling-upgrade versions were accepted")
	}
}

func TestPeerIdentityDerivesRelayIDOutsideMessage(t *testing.T) {
	identity := validPeerIdentity()
	first, err := identity.RelayID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := identity.RelayID()
	if err != nil || second != first || !validRelayID(first) {
		t.Fatalf("Relay IDs = %q %q err = %v", first, second, err)
	}
	identity.PodUID = uuid.NewString()
	changed, err := identity.RelayID()
	if err != nil || changed == first {
		t.Fatalf("changed Relay ID = %q err = %v", changed, err)
	}
	identity.Namespace = "bad namespace"
	if _, err := identity.RelayID(); err == nil {
		t.Fatal("unsafe authenticated peer identity was accepted")
	}
}

func TestCapacityLeaseKeysAndRevocationsFailClosed(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	request := NewRegistrationRequest()
	request.Endpoint = "wss://relay.example.test/tunnel"
	request.State = StateReady
	request.Capacity = Capacity{
		MaximumPhysicalConnections: 1, MaximumLogicalStreams: 1, ActiveLogicalStreams: 2,
	}
	if _, err := Encode(request, now); err == nil {
		t.Fatal("over-capacity registration was accepted")
	}

	response := NewRegistrationResponse()
	response.RelayID, _ = validPeerIdentity().RelayID()
	response.LeaseID = uuid.NewString()
	response.LeaseExpiresAt = now.Add(time.Minute)
	response.HeartbeatAfter = time.Minute
	response.DesiredState = StateReady
	response.Keys = testKeySet(t, now)
	if _, err := Encode(response, now); err == nil {
		t.Fatal("heartbeat interval equal to lease lifetime was accepted")
	}

	response.HeartbeatAfter = 10 * time.Second
	response.Keys.Keys[0].NotBefore = now.Add(time.Minute)
	if _, err := Encode(response, now); err == nil {
		t.Fatal("key set without an active verification key was accepted")
	}

	response.Keys = testKeySet(t, now)
	response.Revocations = RevocationSummary{
		Generation: 1, SHA256: "not-a-digest", GeneratedAt: now,
		ValidUntil: now.Add(time.Minute), Sessions: []RevokedSession{},
	}
	if _, err := Encode(response, now); err == nil {
		t.Fatal("invalid revocation summary was accepted")
	}
}

func TestRevocationSummaryIsCanonicalAndGenerationBound(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	first, second := uuid.NewString(), uuid.NewString()
	summary, err := NewRevocationSummary(7, []RevokedSession{
		{SessionSHA256: HashSessionID(second), MaximumGeneration: 4, ExpiresAt: now.Add(time.Minute)},
		{SessionSHA256: HashSessionID(first), MaximumGeneration: 2, ExpiresAt: now.Add(time.Minute)},
	}, now, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Revokes(first, 2, now) || summary.Revokes(first, 3, now) ||
		!summary.Revokes(second, 4, now) || summary.Revokes(uuid.NewString(), 1, now) {
		t.Fatalf("revocation lookup failed: %#v", summary)
	}
	tampered := summary
	tampered.Sessions = append([]RevokedSession(nil), summary.Sessions...)
	tampered.Sessions[0].MaximumGeneration++
	if err := tampered.Validate(now); err == nil {
		t.Fatal("tampered revocation summary digest was accepted")
	}
}

func validPeerIdentity() PeerIdentity {
	return PeerIdentity{
		TrustDomain: "cluster.local", Namespace: "kubeloop-system",
		ServiceAccount: "kubeloop-data-plane", PodUID: uuid.NewString(),
	}
}

func testKeySet(t *testing.T, now time.Time) VerificationKeySet {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	return VerificationKeySet{Generation: 1, Keys: []VerificationKey{{
		ID: "primary", Algorithm: "EdDSA",
		PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})),
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
	}}}
}

package relayregistry

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func TestRegistrationHeartbeatGenerationGateAndNoSilentReassignment(
	t *testing.T,
) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)
	identity := peer("zone-a", "pod-a")
	registration := registrationRequest("a.example.test", 100, 0)
	registered, err := registry.Register(identity, registration)
	if err != nil {
		t.Fatal(err)
	}
	expectedRelayID, _ := identity.RelayID()
	if registered.RelayID != expectedRelayID || registered.LeaseID == "" ||
		registered.SelectedVersion != relaycontrol.APIVersion {
		t.Fatalf("registration = %#v", registered)
	}
	allocation := allocationRequest("zone-a")
	if _, err := registry.Allocate(allocation); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatalf("allocation before key acknowledgement error = %v", err)
	}
	heartbeat := heartbeatRequest(registered.LeaseID, 100, 1)
	if _, err := registry.Heartbeat(identity, heartbeat); err != nil {
		t.Fatal(err)
	}
	assigned, err := registry.Allocate(allocation)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.RelayID != registered.RelayID ||
		assigned.LeaseID != registered.LeaseID {
		t.Fatalf("assignment = %#v", assigned)
	}
	newGeneration := allocation
	newGeneration.Generation = 2
	reused, err := registry.Allocate(newGeneration)
	if err != nil || reused != assigned {
		t.Fatalf("generation update assignment = %#v err = %v", reused, err)
	}
	statuses := registry.Snapshot()
	if len(statuses) != 1 || statuses[0].Reservations != 1 {
		t.Fatalf("generation update duplicated reservation: %#v", statuses)
	}
	if _, err := registry.Allocate(allocation); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Session generation error = %v", err)
	}
	changedNetwork := newGeneration
	changedNetwork.NetworkSpecHash = strings.Repeat("ab", 32)
	if _, err := registry.Allocate(changedNetwork); !errors.Is(
		err,
		ErrConflict,
	) {
		t.Fatalf("same-generation changed Session NetworkSpec error = %v", err)
	}
	changedNetwork.Generation = 3
	refreshed, err := registry.Allocate(changedNetwork)
	if err != nil || refreshed != assigned {
		t.Fatalf(
			"new-generation changed Session NetworkSpec assignment = %#v err = %v",
			refreshed,
			err,
		)
	}
	statuses = registry.Snapshot()
	if len(statuses) != 1 || statuses[0].Reservations != 1 {
		t.Fatalf("NetworkSpec refresh duplicated reservation: %#v", statuses)
	}

	replacement := registrationRequest("a.example.test", 100, 1)
	reregistered, err := registry.Register(identity, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if reregistered.LeaseID == registered.LeaseID {
		t.Fatal("re-registration reused a lease")
	}
	if _, err := registry.Heartbeat(identity, heartbeat); !errors.Is(
		err,
		ErrConflict,
	) {
		t.Fatalf("old lease heartbeat error = %v", err)
	}
	if _, err := registry.Allocate(changedNetwork); !errors.Is(
		err,
		ErrAssignedRelayUnavailable,
	) {
		t.Fatalf(
			"Session was reassigned before the replacement Relay acknowledged control state: %v",
			err,
		)
	}
	replacementHeartbeat := heartbeatRequest(reregistered.LeaseID, 100, 1)
	if _, err := registry.Heartbeat(identity, replacementHeartbeat); err != nil {
		t.Fatal(err)
	}
	changedNetwork.Generation = 4
	reassigned, err := registry.Allocate(changedNetwork)
	if err != nil {
		t.Fatalf("reassign newer Session generation: %v", err)
	}
	if reassigned.RelayID != reregistered.RelayID ||
		reassigned.LeaseID != reregistered.LeaseID {
		t.Fatalf("replacement assignment = %#v", reassigned)
	}
}

func TestAllocationNegotiatesTrafficEncryptionCapability(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)

	legacyIdentity := peer("zone-a", "legacy")
	registerAndAcknowledge(t, registry, legacyIdentity, "legacy.example.test", 10, 0)

	encryptedIdentity := peer("zone-a", "encrypted")
	registration := registrationRequest("encrypted.example.test", 10, 0)
	registration.SupportedVersions = []string{
		relaycontrol.APIVersionV2, relaycontrol.APIVersionV1,
	}
	registered, err := registry.Register(encryptedIdentity, registration)
	if err != nil || registered.SelectedVersion != relaycontrol.APIVersionV2 {
		t.Fatalf("encrypted registration = %#v err = %v", registered, err)
	}
	heartbeat := heartbeatRequest(registered.LeaseID, 10, 1)
	heartbeat.APIVersion = relaycontrol.APIVersionV2
	heartbeat.TrafficEncryption = new(true)
	heartbeat.NoisePublicKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := registry.Heartbeat(encryptedIdentity, heartbeat); err != nil {
		t.Fatal(err)
	}

	encryptedRequest := allocationRequest("zone-a")
	encryptedRequest.TrafficEncryption = new(true)
	encrypted, err := registry.Allocate(encryptedRequest)
	if err != nil || encrypted.RelayID != registered.RelayID ||
		!encrypted.TrafficEncryption || encrypted.NoisePublicKey != heartbeat.NoisePublicKey {
		t.Fatalf("encrypted assignment = %#v err = %v", encrypted, err)
	}

	plaintextRequest := allocationRequest("zone-a")
	plaintextRequest.TrafficEncryption = new(false)
	plaintext, err := registry.Allocate(plaintextRequest)
	legacyRelayID, _ := legacyIdentity.RelayID()
	if err != nil || plaintext.RelayID != legacyRelayID ||
		plaintext.TrafficEncryption || plaintext.NoisePublicKey != "" {
		t.Fatalf("plaintext assignment = %#v err = %v", plaintext, err)
	}
}

func TestAllocationPrefersTrustedTopologyThenLowerLoadAndHonorsDrain(
	t *testing.T,
) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)
	zoneA, zoneB := peer("zone-a", "pod-a"), peer("zone-b", "pod-b")
	a := registerAndAcknowledge(t, registry, zoneA, "a.example.test", 10, 1)
	b := registerAndAcknowledge(t, registry, zoneB, "b.example.test", 10, 7)

	topologyAssignment, err := registry.Allocate(allocationRequest("zone-b"))
	if err != nil {
		t.Fatal(err)
	}
	if topologyAssignment.RelayID != b.RelayID {
		t.Fatalf(
			"topology assignment Relay = %q, want %q",
			topologyAssignment.RelayID,
			b.RelayID,
		)
	}
	loadAssignmentRequest := allocationRequest("")
	loadAssignment, err := registry.Allocate(loadAssignmentRequest)
	if err != nil {
		t.Fatal(err)
	}
	if loadAssignment.RelayID != a.RelayID {
		t.Fatalf(
			"load assignment Relay = %q, want %q",
			loadAssignment.RelayID,
			a.RelayID,
		)
	}
	if err := registry.SetDesiredState(a.RelayID, relaycontrol.StateDraining); err != nil {
		t.Fatal(err)
	}
	drainHeartbeat := heartbeatRequest(a.LeaseID, 10, 1)
	drainResponse, err := registry.Heartbeat(zoneA, drainHeartbeat)
	if err != nil || drainResponse.DesiredState != relaycontrol.StateDraining {
		t.Fatalf("drain heartbeat = %#v err = %v", drainResponse, err)
	}
	newAssignment, err := registry.Allocate(allocationRequest("zone-a"))
	if err != nil {
		t.Fatal(err)
	}
	if newAssignment.RelayID != b.RelayID {
		t.Fatalf("draining Relay received new assignment: %#v", newAssignment)
	}
	if _, err := registry.Allocate(loadAssignmentRequest); !errors.Is(
		err,
		ErrAssignedRelayUnavailable,
	) {
		t.Fatalf("existing assignment was reported as migrated: %v", err)
	}
}

func TestLeaseExpiryAndControlGenerationStopNewAllocations(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)
	identity := peer("zone-a", "pod-a")
	registered := registerAndAcknowledge(
		t,
		registry,
		identity,
		"a.example.test",
		10,
		1,
	)
	clock.Advance(46 * time.Second)
	if _, err := registry.Allocate(allocationRequest("")); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatalf("expired Relay allocation error = %v", err)
	}
	if _, err := registry.Heartbeat(identity, heartbeatRequest(registered.LeaseID, 10, 1)); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("expired Relay heartbeat error = %v", err)
	}
	statuses := registry.Snapshot()
	if len(statuses) != 1 || statuses[0].Online {
		t.Fatalf("expired Relay statuses = %#v", statuses)
	}

	clock = &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry = newTestRegistry(t, clock, 100)
	registered = registerAndAcknowledge(
		t,
		registry,
		identity,
		"a.example.test",
		10,
		1,
	)
	keys := verificationKeys(t, clock.Now(), 2)
	if err := registry.UpdateControlPlaneState(keys, relaycontrol.RevocationSummary{}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Allocate(allocationRequest("")); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatalf("stale key generation allocation error = %v", err)
	}
	heartbeat := heartbeatRequest(registered.LeaseID, 10, 2)
	if _, err := registry.Heartbeat(identity, heartbeat); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Allocate(allocationRequest("")); err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateControlPlaneState(
		verificationKeys(t, clock.Now(), 1),
		relaycontrol.RevocationSummary{},
	); err == nil {
		t.Fatal("control-plane key generation moved backwards")
	}
}

func TestConcurrentAllocationNeverExceedsCapacity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 5)
	registerAndAcknowledge(
		t,
		registry,
		peer("zone-a", "pod-a"),
		"a.example.test",
		5,
		0,
	)

	var wait sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for range 30 {
		wait.Go(func() {
			if _, err := registry.Allocate(allocationRequest("")); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			} else if !errors.Is(err, ErrUnavailable) {
				t.Errorf("allocation error = %v", err)
			}
		})
	}
	wait.Wait()
	if succeeded != 5 {
		t.Fatalf("successful allocations = %d, want 5", succeeded)
	}
	statuses := registry.Snapshot()
	if len(statuses) != 1 || statuses[0].Reservations != 5 {
		t.Fatalf("Relay status = %#v", statuses)
	}
}

func TestReleaseFencesGenerationAndRestoresRelayCapacity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 1)
	registered := registerAndAcknowledge(
		t,
		registry,
		peer("zone-a", "pod-a"),
		"a.example.test",
		1,
		0,
	)
	request := allocationRequest("zone-a")
	assigned, err := registry.Allocate(request)
	if err != nil || assigned.RelayID != registered.RelayID {
		t.Fatalf("assignment = %#v err = %v", assigned, err)
	}
	if registry.Release(request.SessionID, request.Generation+1) {
		t.Fatal("mismatched generation released an assignment")
	}
	if statuses := registry.Snapshot(); len(statuses) != 1 || statuses[0].Reservations != 1 {
		t.Fatalf("status after fenced release = %#v", statuses)
	}
	if !registry.Release(request.SessionID, request.Generation) {
		t.Fatal("matching generation did not release the assignment")
	}
	if registry.Release(request.SessionID, request.Generation) {
		t.Fatal("released assignment was released twice")
	}
	if statuses := registry.Snapshot(); len(statuses) != 1 || statuses[0].Reservations != 0 {
		t.Fatalf("status after release = %#v", statuses)
	}
	if _, err := registry.Allocate(allocationRequest("zone-a")); err != nil {
		t.Fatalf("released capacity was not reusable: %v", err)
	}
}

func TestRestoreDesiredStateAppliesOfflineAndOnlineIntent(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 10)
	identity := peer("zone-a", "pod-a")
	relayID, err := identity.RelayID()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RestoreDesiredState(relayID, relaycontrol.StateDraining); err != nil {
		t.Fatal(err)
	}
	registered, err := registry.Register(identity, registrationRequest("a.example.test", 10, 0))
	if err != nil || registered.DesiredState != relaycontrol.StateDraining {
		t.Fatalf("registration = %#v err = %v", registered, err)
	}
	heartbeat := heartbeatRequest(registered.LeaseID, 10, 1)
	response, err := registry.Heartbeat(identity, heartbeat)
	if err != nil || response.DesiredState != relaycontrol.StateDraining {
		t.Fatalf("draining heartbeat = %#v err = %v", response, err)
	}
	if _, err := registry.Allocate(allocationRequest("zone-a")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("draining Relay allocation error = %v", err)
	}
	if err := registry.RestoreDesiredState(relayID, relaycontrol.StateReady); err != nil {
		t.Fatal(err)
	}
	response, err = registry.Heartbeat(identity, heartbeat)
	if err != nil || response.DesiredState != relaycontrol.StateReady {
		t.Fatalf("ready heartbeat = %#v err = %v", response, err)
	}
	if _, err := registry.Allocate(allocationRequest("zone-a")); err != nil {
		t.Fatalf("restored ready Relay was unavailable: %v", err)
	}
	if err := registry.RestoreDesiredState("", relaycontrol.StateReady); err == nil {
		t.Fatal("empty durable Relay identity was accepted")
	}
	if err := registry.RestoreDesiredState(relayID, relaycontrol.State("invalid")); err == nil {
		t.Fatal("invalid durable desired state was accepted")
	}
}

func TestEndpointPolicyUsesAuthenticatedIdentity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	keys := verificationKeys(t, clock.Now(), 1)
	registry, err := New(Config{
		Now: clock.Now, TicketIssuer: "https://control-plane.example.test", VerificationKeys: keys,
		EndpointPolicy: func(identity relaycontrol.PeerIdentity, endpoint string) error {
			if identity.Namespace != "kubeloop-system" ||
				!strings.Contains(endpoint, identity.PodUID) {
				return errors.New(
					"endpoint does not belong to authenticated Pod",
				)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := peer("zone-a", "pod-a")
	if _, err := registry.Register(identity, registrationRequest("attacker.example.test", 10, 0)); err == nil {
		t.Fatal("untrusted advertised endpoint was accepted")
	}
	request := registrationRequest(identity.PodUID+".relay.example.test", 10, 0)
	if _, err := registry.Register(identity, request); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryAllowsHTTPTicketIssuer(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	_, err := New(Config{
		Now: clock.Now, TicketIssuer: "http://control-plane.example.test",
		VerificationKeys: verificationKeys(t, clock.Now(), 1),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newTestRegistry(
	t *testing.T,
	clock *testClock,
	maximumStreams uint32,
) *Registry {
	t.Helper()
	registry, err := New(Config{
		Now: clock.Now, LeaseDuration: 45 * time.Second, HeartbeatAfter: 10 * time.Second,
		TicketIssuer:     "https://control-plane.example.test",
		VerificationKeys: verificationKeys(t, clock.Now(), 1),
		EndpointPolicy: func(_ relaycontrol.PeerIdentity, endpoint string) error {
			if !strings.HasSuffix(endpoint, ".example.test/tunnel") {
				return errors.New("endpoint is outside the configured domain")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = maximumStreams
	return registry
}

func peer(zone, pod string) relaycontrol.PeerIdentity {
	return relaycontrol.PeerIdentity{
		TrustDomain: "cluster.local", Namespace: "kubeloop-system",
		ServiceAccount: "kubeloop-data-plane", PodUID: pod,
		Topology: map[string]string{"topology.kubernetes.io/zone": zone},
	}
}

func registrationRequest(
	host string,
	maximumStreams, activeStreams uint32,
) relaycontrol.RegistrationRequest {
	request := relaycontrol.NewRegistrationRequest()
	request.Endpoint = "wss://" + host + "/tunnel"
	request.State = relaycontrol.StateReady
	request.Capacity = relaycontrol.Capacity{
		MaximumPhysicalConnections: 100, MaximumLogicalStreams: maximumStreams,
		ActivePhysicalConnections: 1, ActiveLogicalStreams: activeStreams,
	}
	request.AppliedKeyGeneration = 0
	return request
}

func heartbeatRequest(
	leaseID string,
	maximumStreams, keyGeneration uint32,
) relaycontrol.HeartbeatRequest {
	request := relaycontrol.NewHeartbeatRequest()
	request.LeaseID = leaseID
	request.State = relaycontrol.StateReady
	request.Capacity = relaycontrol.Capacity{
		MaximumPhysicalConnections: 100, MaximumLogicalStreams: maximumStreams,
		ActivePhysicalConnections: 1,
	}
	request.AppliedKeyGeneration = uint64(keyGeneration)
	return request
}

func allocationRequest(zone string) relaycontrol.AllocationRequest {
	request := relaycontrol.NewAllocationRequest()
	request.SessionID = uuid.NewString()
	request.Generation = 1
	request.NetworkSpecHash = hex.EncodeToString(make([]byte, 32))
	if zone != "" {
		request.Topology = map[string]string{
			"topology.kubernetes.io/zone": zone,
		}
	}
	return request
}

func registerAndAcknowledge(
	t *testing.T,
	registry *Registry,
	identity relaycontrol.PeerIdentity,
	host string,
	maximumStreams, activeStreams uint32,
) relaycontrol.RegistrationResponse {
	t.Helper()
	request := registrationRequest(host, maximumStreams, activeStreams)
	registered, err := registry.Register(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := heartbeatRequest(registered.LeaseID, maximumStreams, 1)
	heartbeat.Capacity.ActiveLogicalStreams = activeStreams
	if _, err := registry.Heartbeat(identity, heartbeat); err != nil {
		t.Fatal(err)
	}
	return registered
}

func verificationKeys(
	t *testing.T,
	now time.Time,
	generation uint64,
) relaycontrol.VerificationKeySet {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	return relaycontrol.VerificationKeySet{
		Generation: generation,
		Keys: []relaycontrol.VerificationKey{{
			ID: fmt.Sprintf("key-%d", generation), Algorithm: "EdDSA",
			PublicKey: string(
				pem.EncodeToMemory(
					&pem.Block{Type: "PUBLIC KEY", Bytes: encoded},
				),
			),
			NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		}},
	}
}

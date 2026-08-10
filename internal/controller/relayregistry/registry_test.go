package relayregistry

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/google/uuid"
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

func TestRegistrationHeartbeatGenerationGateAndNoSilentReassignment(t *testing.T) {
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
	if _, err := registry.Allocate(allocation); !errors.Is(err, ErrUnavailable) {
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
	if assigned.RelayID != registered.RelayID || assigned.LeaseID != registered.LeaseID {
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
	changedNetwork.Generation = 3
	changedNetwork.NetworkSpecHash = strings.Repeat("ab", 32)
	if _, err := registry.Allocate(changedNetwork); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed Session NetworkSpec error = %v", err)
	}

	replacement := registrationRequest("a.example.test", 100, 1)
	reregistered, err := registry.Register(identity, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if reregistered.LeaseID == registered.LeaseID {
		t.Fatal("re-registration reused a lease")
	}
	if _, err := registry.Heartbeat(identity, heartbeat); !errors.Is(err, ErrConflict) {
		t.Fatalf("old lease heartbeat error = %v", err)
	}
	newGeneration.Generation = 3
	if _, err := registry.Allocate(newGeneration); !errors.Is(err, ErrAssignedRelayUnavailable) {
		t.Fatalf("existing Session was silently reassigned: %v", err)
	}
}

func TestAllocationPrefersTrustedTopologyThenLowerLoadAndHonorsDrain(t *testing.T) {
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
		t.Fatalf("topology assignment Relay = %q, want %q", topologyAssignment.RelayID, b.RelayID)
	}
	loadAssignmentRequest := allocationRequest("")
	loadAssignment, err := registry.Allocate(loadAssignmentRequest)
	if err != nil {
		t.Fatal(err)
	}
	if loadAssignment.RelayID != a.RelayID {
		t.Fatalf("load assignment Relay = %q, want %q", loadAssignment.RelayID, a.RelayID)
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
	if _, err := registry.Allocate(loadAssignmentRequest); !errors.Is(err, ErrAssignedRelayUnavailable) {
		t.Fatalf("existing assignment was reported as migrated: %v", err)
	}
}

func TestLeaseExpiryAndControlGenerationStopNewAllocations(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)
	identity := peer("zone-a", "pod-a")
	registered := registerAndAcknowledge(t, registry, identity, "a.example.test", 10, 1)
	clock.Advance(46 * time.Second)
	if _, err := registry.Allocate(allocationRequest("")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired Relay allocation error = %v", err)
	}
	if _, err := registry.Heartbeat(identity, heartbeatRequest(registered.LeaseID, 10, 1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired Relay heartbeat error = %v", err)
	}
	statuses := registry.Snapshot()
	if len(statuses) != 1 || statuses[0].Online {
		t.Fatalf("expired Relay statuses = %#v", statuses)
	}

	clock = &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry = newTestRegistry(t, clock, 100)
	registered = registerAndAcknowledge(t, registry, identity, "a.example.test", 10, 1)
	keys := verificationKeys(t, clock.Now(), 2)
	if err := registry.UpdateControlPlaneState(keys, relaycontrol.RevocationSummary{}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Allocate(allocationRequest("")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale key generation allocation error = %v", err)
	}
	heartbeat := heartbeatRequest(registered.LeaseID, 10, 2)
	if _, err := registry.Heartbeat(identity, heartbeat); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Allocate(allocationRequest("")); err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateControlPlaneState(verificationKeys(t, clock.Now(), 1), relaycontrol.RevocationSummary{}); err == nil {
		t.Fatal("control-plane key generation moved backwards")
	}
}

func TestConcurrentAllocationNeverExceedsCapacity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 5)
	registerAndAcknowledge(t, registry, peer("zone-a", "pod-a"), "a.example.test", 5, 0)

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

func TestEndpointPolicyUsesAuthenticatedIdentity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	keys := verificationKeys(t, clock.Now(), 1)
	registry, err := New(Config{
		Now: clock.Now, VerificationKeys: keys,
		EndpointPolicy: func(identity relaycontrol.PeerIdentity, endpoint string) error {
			if identity.Namespace != "kubeloop-system" || !strings.Contains(endpoint, identity.PodUID) {
				return errors.New("endpoint does not belong to authenticated Pod")
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

func newTestRegistry(t *testing.T, clock *testClock, maximumStreams uint32) *Registry {
	t.Helper()
	registry, err := New(Config{
		Now: clock.Now, LeaseDuration: 45 * time.Second, HeartbeatAfter: 10 * time.Second,
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

func registrationRequest(host string, maximumStreams, activeStreams uint32) relaycontrol.RegistrationRequest {
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

func heartbeatRequest(leaseID string, maximumStreams, keyGeneration uint32) relaycontrol.HeartbeatRequest {
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
		request.Topology = map[string]string{"topology.kubernetes.io/zone": zone}
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

func verificationKeys(t *testing.T, now time.Time, generation uint64) relaycontrol.VerificationKeySet {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	return relaycontrol.VerificationKeySet{Generation: generation, Keys: []relaycontrol.VerificationKey{{
		ID: fmt.Sprintf("key-%d", generation), Algorithm: "EdDSA",
		PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})),
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
	}}}
}

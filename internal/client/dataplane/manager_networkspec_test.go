package dataplane

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

func TestManagerReconfiguresTUNWhenHeartbeatRefreshesNetworkSpec(t *testing.T) {
	fixture := newNetworkSpecRefreshFixture(t)
	firstStatus, firstCore, transportDone := fixture.start(t)
	fixture.publishRefresh(t, transportDone)
	fixture.releaseRefresh()
	secondControl := receiveControl(t, fixture.controls)
	t.Cleanup(func() { checkTestClose(t, secondControl.Close) })
	fixture.assertRecovered(t, firstStatus, firstCore)
}

type networkSpecRefreshFixture struct {
	manager        *Manager
	tickets        *testTickets
	tunStarter     *testTUNStarter
	controls       chan net.Conn
	statusEvents   chan StatusEvent
	refreshStarted chan struct{}
	allowRefresh   chan struct{}
	releaseOnce    sync.Once
	starts         atomic.Int32
	profile        profile.Profile
	initialSession remote.Session
	refreshedSpec  networkspec.Spec
	refreshedHash  string
}

func newNetworkSpecRefreshFixture(t *testing.T) *networkSpecRefreshFixture {
	t.Helper()
	initialSpec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.42.0.0/16"}, ServiceIPs: []string{"10.96.0.10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	initialHash, _ := networkspec.Hash(initialSpec)
	refreshedSpec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.42.0.0/16"}, PodIPs: []string{"10.42.7.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	refreshedHash, _ := networkspec.Hash(refreshedSpec)
	fixture := &networkSpecRefreshFixture{
		tunStarter:     &testTUNStarter{},
		controls:       make(chan net.Conn, 4),
		statusEvents:   make(chan StatusEvent, 8),
		refreshStarted: make(chan struct{}, 1),
		allowRefresh:   make(chan struct{}),
		profile: profile.Profile{
			ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath,
		},
		refreshedSpec: refreshedSpec,
		refreshedHash: refreshedHash,
	}
	fixture.initialSession = remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: dataplaneSessionActive,
		Generation: 4, NetworkSpec: initialSpec, NetworkSpecHash: initialHash,
	}
	fixture.tickets = &testTickets{session: fixture.initialSession, updates: make(chan remote.SessionUpdate, 1)}
	fixture.tickets.refresh = func(current remote.Session) (remote.Session, error) {
		select {
		case fixture.refreshStarted <- struct{}{}:
		default:
		}
		<-fixture.allowRefresh
		return current, nil
	}
	fixture.manager, err = NewManager(fixture.tickets, Config{
		RecoveryAttempts: 2, RecoveryBackoff: 10 * time.Millisecond,
		TUNStarter: fixture.tunStarter, OnStatus: func(event StatusEvent) { fixture.statusEvents <- event },
		startForwarder: func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := clientConfig.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			fixture.starts.Add(1)
			go acceptTestControlWithSignal(listener, fixture.controls)
			return &testForwarder{Listener: listener}, nil
		},
		listenSOCKS: func(context.Context, string) (localBridge, error) {
			return &testBridge{
				address: testAddress("127.0.0.1:" + strconv.Itoa(47000+int(fixture.starts.Load()))),
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fixture.releaseRefresh)
	t.Cleanup(func() { _ = fixture.manager.Shutdown() })
	return fixture
}

func (fixture *networkSpecRefreshFixture) releaseRefresh() {
	fixture.releaseOnce.Do(func() { close(fixture.allowRefresh) })
}

func (fixture *networkSpecRefreshFixture) start(t *testing.T) (Status, *testCore, <-chan struct{}) {
	t.Helper()
	firstStatus, err := fixture.manager.Connect(context.Background(), fixture.profile, fixture.initialSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.StartTUN(context.Background(), fixture.profile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, firstCore := fixture.tunStarter.snapshot()
	firstControl := receiveControl(t, fixture.controls)
	t.Cleanup(func() { checkTestClose(t, firstControl.Close) })
	fixture.manager.mu.Lock()
	transportDone := fixture.manager.active[fixture.profile.ID].runtime.TransportDone()
	fixture.manager.mu.Unlock()
	return firstStatus, firstCore, transportDone
}

func (fixture *networkSpecRefreshFixture) publishRefresh(t *testing.T, transportDone <-chan struct{}) {
	t.Helper()
	fixture.tickets.mu.Lock()
	fixture.tickets.session.Generation++
	fixture.tickets.session.NetworkSpec = fixture.refreshedSpec
	fixture.tickets.session.NetworkSpecHash = fixture.refreshedHash
	updatedSession := fixture.tickets.session
	fixture.tickets.mu.Unlock()
	fixture.tickets.updates <- remote.SessionUpdate{ProfileID: fixture.profile.ID, Session: updatedSession}
	refreshEvent := waitStatusEvent(t, fixture.statusEvents, reasonNetworkSpecChanged)
	if refreshEvent.Status.State != dataplaneReconnecting || !refreshEvent.Retryable {
		t.Fatalf("NetworkSpec refresh event = %#v", refreshEvent)
	}
	select {
	case <-fixture.refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("NetworkSpec refresh did not start")
	}
	select {
	case <-transportDone:
		t.Fatal("NetworkSpec refresh interrupted the active transport before its replacement was ready")
	default:
	}
}

func waitStatusEvent(t *testing.T, events <-chan StatusEvent, reason string) StatusEvent {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Reason == reason {
				return event
			}
		case <-deadline:
			t.Fatalf("status event %q was not published", reason)
		}
	}
}

func (fixture *networkSpecRefreshFixture) assertRecovered(
	t *testing.T,
	firstStatus Status,
	firstCore *testCore,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var recovered Status
	for {
		fixture.manager.mu.Lock()
		entry := fixture.manager.active[fixture.profile.ID]
		ready := entry != nil && !entry.recovering &&
			entry.session.Generation == fixture.initialSession.Generation+1 &&
			entry.session.NetworkSpecHash == fixture.refreshedHash && entry.runtime.Status().Mode == ModeTUN
		if entry != nil {
			recovered = entry.runtime.Status()
		}
		fixture.manager.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Data Plane did not replace the Runtime for the refreshed NetworkSpec")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if recovered.SOCKSAddress != firstStatus.SOCKSAddress {
		t.Fatalf(
			"refreshed NetworkSpec changed stable SOCKS endpoint from %q to %q",
			firstStatus.SOCKSAddress,
			recovered.SOCKSAddress,
		)
	}
	if fixture.starts.Load() != 2 {
		t.Fatalf("transport starts = %d", fixture.starts.Load())
	}
	tunStarts, tunNetwork, tunBridge, _, recoveredCore := fixture.tunStarter.snapshot()
	if tunStarts != 2 || recoveredCore == firstCore || tunBridge != recovered.SOCKSAddress ||
		len(tunNetwork.PodIPs) != 1 || tunNetwork.PodIPs[0] != "10.42.7.9" || len(tunNetwork.ServiceIPs) != 0 {
		t.Fatalf(
			"refreshed TUN = starts %d network %#v bridge %q core-reused %t",
			tunStarts,
			tunNetwork,
			tunBridge,
			recoveredCore == firstCore,
		)
	}
	select {
	case <-firstCore.Done():
	default:
		t.Fatal("stale TUN remained active after the NetworkSpec refresh")
	}
}

func TestManagerRejectsStaleRecoveryGeneration(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: dataplaneSessionActive,
		Generation: 5, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 2)
	refreshed := make(chan struct{}, 1)
	statusEvents := make(chan StatusEvent, 8)
	var starts atomic.Int32
	tunStarter := &testTUNStarter{}
	tickets := &testTickets{session: session}
	tickets.refresh = func(current remote.Session) (remote.Session, error) {
		current.Generation--
		refreshed <- struct{}{}
		return current, nil
	}
	manager, err := NewManager(tickets, Config{
		RecoveryAttempts: 1, RecoveryBackoff: time.Millisecond,
		TUNStarter: tunStarter,
		OnStatus:   func(event StatusEvent) { statusEvents <- event },
		startForwarder: func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := clientConfig.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			starts.Add(1)
			go acceptTestControlWithSignal(listener, controls)
			return &testForwarder{Listener: listener}, nil
		},
		listenSOCKS: func(context.Context, string) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:45001")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{
		ID:         "service",
		BaseURL:    "https://gateway.example.test",
		TunnelPath: defaultTunnelPath,
	}
	if _, err := manager.Connect(context.Background(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, terminalCore := tunStarter.snapshot()
	_ = receiveControl(t, controls).Close()
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("recovery did not refresh the Session")
	}
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		entry := manager.active[serverProfile.ID]
		finished := entry != nil && !entry.recovering && entry.lastError != nil
		message := ""
		if finished {
			message = entry.lastError.Error()
		}
		manager.mu.Unlock()
		if finished {
			if !strings.Contains(message, "stale Session generation") {
				t.Fatalf("recovery error = %q", message)
			}
			manager.mu.Lock()
			bridge := manager.active[serverProfile.ID].runtime.bridge.(*testBridge)
			manager.mu.Unlock()
			forwardSet, closed := bridge.snapshot()
			if forwardSet || !closed {
				t.Fatalf("exhausted recovery bridge state = forward %t closed %t", forwardSet, closed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale recovery did not terminate")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if starts.Load() != 1 {
		t.Fatalf("stale generation started replacement transport: %d", starts.Load())
	}
	var terminal StatusEvent
	eventDeadline := time.After(time.Second)
	for terminal.Status.State != dataplaneError {
		select {
		case terminal = <-statusEvents:
		case <-eventDeadline:
			t.Fatal("terminal Data Plane status event was not published")
		}
	}
	if terminal.ProfileID != serverProfile.ID || !strings.Contains(terminal.Error, "stale Session generation") ||
		terminal.Status.SOCKSAddress == "" || terminal.Reason != reasonSessionChanged || !terminal.Retryable {
		t.Fatalf("terminal Data Plane event = %#v", terminal)
	}
	select {
	case <-terminalCore.Done():
	default:
		t.Fatal("terminal recovery failure did not stop the local TUN")
	}
	_ = manager.Shutdown()
}

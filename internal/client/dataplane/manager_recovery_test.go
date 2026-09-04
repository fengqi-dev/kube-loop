package dataplane

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func TestManagerRecoversControlStreamWithFreshSessionGeneration(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: dataplaneSessionActive,
		Generation: 4, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 4)
	var starts atomic.Int32
	tunStarter := &testTUNStarter{}
	tickets := &testTickets{session: session}
	tickets.refresh = func(current remote.Session) (remote.Session, error) {
		current.Generation++
		return current, nil
	}
	manager, err := NewManager(tickets, Config{
		RecoveryAttempts: 2, RecoveryBackoff: 10 * time.Millisecond,
		TUNStarter: tunStarter,
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
		listenSOCKS: func(_ context.Context, _, _ string, _ tunnel.SessionToken) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:" + strconv.Itoa(44000+int(starts.Load())))}, nil
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
	first, err := manager.Connect(context.Background(), serverProfile, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, firstCore := tunStarter.snapshot()
	firstControl := receiveControl(t, controls)
	_ = firstControl.Close()
	secondControl := receiveControl(t, controls)
	defer checkTestClose(t, secondControl.Close)

	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		entry := manager.active[serverProfile.ID]
		ready := entry != nil && !entry.recovering && entry.session.Generation == session.Generation+1 &&
			entry.runtime.Status().SOCKSAddress == first.SOCKSAddress && entry.runtime.Status().Mode == ModeTUN
		manager.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Data Plane did not publish the recovered Session generation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if starts.Load() != 2 {
		t.Fatalf("transport starts = %d", starts.Load())
	}
	tunStarts, _, _, _, recoveredCore := tunStarter.snapshot()
	if tunStarts != 1 || recoveredCore != firstCore {
		t.Fatalf("TUN recovery starts = %d, preserved core = %t", tunStarts, recoveredCore == firstCore)
	}
	select {
	case <-firstCore.Done():
		t.Fatal("TUN was reinstalled during transport-only recovery")
	default:
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSystemResumeRefreshesTransportWithoutReinstallingTUN(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: dataplaneSessionActive,
		Generation: 4, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 4)
	statusEvents := make(chan StatusEvent, 8)
	var starts atomic.Int32
	tunStarter := &testTUNStarter{}
	tickets := &testTickets{session: session}
	manager, err := NewManager(tickets, Config{
		RecoveryAttempts: 2, RecoveryBackoff: 10 * time.Millisecond,
		TUNStarter: tunStarter, OnStatus: func(event StatusEvent) { statusEvents <- event },
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
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:48001")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	serverProfile := profile.Profile{
		ID:         "service",
		BaseURL:    "https://gateway.example.test",
		TunnelPath: defaultTunnelPath,
	}
	initial, err := manager.Connect(context.Background(), serverProfile, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, initialCore := tunStarter.snapshot()
	firstControl := receiveControl(t, controls)
	defer checkTestClose(t, firstControl.Close)

	if resumed := manager.ResumeAll(); resumed != 1 {
		t.Fatalf("resumed Profiles = %d, want 1", resumed)
	}
	secondControl := receiveControl(t, controls)
	defer checkTestClose(t, secondControl.Close)

	deadline := time.Now().Add(time.Second)
	for {
		status, statusErr := manager.Status(serverProfile.ID)
		if statusErr == nil && status.State == dataplaneConnected && status.Mode == ModeTUN &&
			status.SOCKSAddress == initial.SOCKSAddress {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("system resume did not recover stable TUN: status=%#v err=%v", status, statusErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if starts.Load() != 2 {
		t.Fatalf("wake transport starts = %d, want 2", starts.Load())
	}
	tunStarts, _, _, _, resumedCore := tunStarter.snapshot()
	if tunStarts != 1 || resumedCore != initialCore {
		t.Fatalf("wake reinstalled TUN: starts=%d corePreserved=%t", tunStarts, resumedCore == initialCore)
	}
	var wake StatusEvent
	eventDeadline := time.After(time.Second)
	for wake.Reason != reasonSystemResumed {
		select {
		case wake = <-statusEvents:
		case <-eventDeadline:
			t.Fatal("system resume status event was not published")
		}
	}
	if wake.Status.State != dataplaneReconnecting || !wake.Retryable {
		t.Fatalf("system resume status event = %#v", wake)
	}
}

func TestManagerReplacesTransportWhenHeartbeatAdvancesGeneration(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: dataplaneSessionActive,
		Generation: 4, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 4)
	statusEvents := make(chan StatusEvent, 8)
	var starts atomic.Int32
	tunStarter := &testTUNStarter{}
	bridge := &testBridge{address: testAddress("127.0.0.1:48002")}
	tickets := &testTickets{session: session, updates: make(chan remote.SessionUpdate, 1)}
	manager, err := NewManager(tickets, Config{
		RecoveryAttempts: 2, RecoveryBackoff: 10 * time.Millisecond,
		TUNStarter: tunStarter, OnStatus: func(event StatusEvent) { statusEvents <- event },
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
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
			return bridge, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	serverProfile := profile.Profile{
		ID:         "service",
		BaseURL:    "https://gateway.example.test",
		TunnelPath: defaultTunnelPath,
	}
	initial, err := manager.Connect(context.Background(), serverProfile, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, initialCore := tunStarter.snapshot()
	firstControl := receiveControl(t, controls)
	defer checkTestClose(t, firstControl.Close)
	manager.mu.Lock()
	initialTransportDone := manager.active[serverProfile.ID].runtime.TransportDone()
	manager.mu.Unlock()

	tickets.mu.Lock()
	tickets.session.Generation++
	updated := tickets.session
	tickets.mu.Unlock()
	tickets.updates <- remote.SessionUpdate{ProfileID: serverProfile.ID, Session: updated}
	secondControl := receiveControl(t, controls)
	defer checkTestClose(t, secondControl.Close)

	deadline := time.Now().Add(time.Second)
	for {
		status, statusErr := manager.Status(serverProfile.ID)
		if statusErr == nil && status.State == dataplaneConnected && status.Mode == ModeTUN &&
			status.SessionGeneration == updated.Generation && status.SOCKSAddress == initial.SOCKSAddress {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation refresh did not replace transport: status=%#v err=%v", status, statusErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if starts.Load() != 2 {
		t.Fatalf("transport starts = %d, want 2", starts.Load())
	}
	tunStarts, _, _, _, currentCore := tunStarter.snapshot()
	if tunStarts != 1 || currentCore != initialCore {
		t.Fatalf(
			"generation refresh reinstalled TUN: starts=%d corePreserved=%t",
			tunStarts,
			currentCore == initialCore,
		)
	}
	select {
	case <-initialTransportDone:
	default:
		t.Fatal("generation refresh did not retire the old transport")
	}
	wantToken, err := tunnel.RelaySessionToken(updated.ID, updated.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, token := bridge.transport(); token != wantToken {
		t.Fatalf("bridge token = %x, want generation token %x", token, wantToken)
	}
	var refreshEvent StatusEvent
	eventDeadline := time.After(time.Second)
	for refreshEvent.Reason != reasonSessionChanged {
		select {
		case refreshEvent = <-statusEvents:
		case <-eventDeadline:
			t.Fatal("Session generation refresh event was not published")
		}
	}
	if refreshEvent.Status.State != dataplaneReconnecting || !refreshEvent.Retryable {
		t.Fatalf("Session generation refresh event = %#v", refreshEvent)
	}
}

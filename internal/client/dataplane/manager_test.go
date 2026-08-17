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
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type testTickets struct {
	mu      sync.Mutex
	calls   int
	session remote.Session
	refresh func(remote.Session) (remote.Session, error)
}

type testTUNStarter struct {
	mu            sync.Mutex
	starts        int
	network       singbox.NetworkSpec
	bridgeAddress string
	namespace     string
	hosts         []singbox.HostAlias
	core          *testCore
}

func (starter *testTUNStarter) Start(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress, namespace string,
	hosts []singbox.HostAlias,
) (singbox.RunningCore, error) {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	starter.starts++
	starter.network = network
	starter.bridgeAddress = bridgeAddress
	starter.namespace = namespace
	starter.hosts = append([]singbox.HostAlias{}, hosts...)
	starter.core = &testCore{done: make(chan struct{}), sessionID: "tun-session"}
	go func(core *testCore) {
		select {
		case <-ctx.Done():
			_ = core.Close()
		case <-core.Done():
		}
	}(starter.core)
	return starter.core, nil
}

func (starter *testTUNStarter) snapshot() (int, singbox.NetworkSpec, string, string, *testCore) {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	return starter.starts, starter.network, starter.bridgeAddress, starter.namespace, starter.core
}

type testCore struct {
	mu           sync.Mutex
	done         chan struct{}
	closeOnce    sync.Once
	sessionID    string
	dnsNamespace string
	hosts        []singbox.HostAlias
	dnsErr       error
	hostsErr     error
	metricsErr   error
	logsErr      error
	closeErr     error
	closeCalls   int
}

func (core *testCore) Close() error {
	core.mu.Lock()
	core.closeCalls++
	core.mu.Unlock()
	core.closeOnce.Do(func() { close(core.done) })
	return core.closeErr
}
func (core *testCore) Done() <-chan struct{} { return core.done }
func (core *testCore) Err() error            { return nil }
func (core *testCore) Snapshot(context.Context) (singbox.Metrics, error) {
	return singbox.Metrics{ActiveConnections: 2}, core.metricsErr
}
func (core *testCore) TrafficEndpoints() singbox.TrafficEndpoints { return singbox.TrafficEndpoints{} }
func (core *testCore) SessionID() string                          { return core.sessionID }
func (core *testCore) Config() []byte                             { return []byte(`{"version":2}`) }
func (core *testCore) ReadLogs(context.Context) ([]string, error) {
	return []string{"ready"}, core.logsErr
}
func (core *testCore) UpdateDNSNamespace(_ context.Context, namespace string) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.dnsErr != nil {
		return core.dnsErr
	}
	core.dnsNamespace = namespace
	return nil
}
func (core *testCore) UpdateHostAliases(_ context.Context, hosts []singbox.HostAlias) error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.hostsErr != nil {
		return core.hostsErr
	}
	core.hosts = append([]singbox.HostAlias{}, hosts...)
	return nil
}
func (core *testCore) ProbeClusterDNS(context.Context) error { return nil }
func (core *testCore) DNSPort() int                          { return 1053 }
func (core *testCore) InternalDNSPort() int                  { return 1054 }

func (tickets *testTickets) RelayTicketSource(string) func(context.Context) (remote.RelayTicket, error) {
	return func(context.Context) (remote.RelayTicket, error) {
		tickets.mu.Lock()
		defer tickets.mu.Unlock()
		tickets.calls++
		return remote.RelayTicket{Ticket: "relay-ticket"}, nil
	}
}

func (tickets *testTickets) Current(string) (remote.Session, error) {
	tickets.mu.Lock()
	defer tickets.mu.Unlock()
	return tickets.session, nil
}

func (tickets *testTickets) Refresh(context.Context, string) (remote.Session, error) {
	tickets.mu.Lock()
	defer tickets.mu.Unlock()
	if tickets.refresh != nil {
		next, err := tickets.refresh(tickets.session)
		if err == nil {
			tickets.session = next
		}
		return next, err
	}
	return tickets.session, nil
}

func TestRuntimeConfigUsesProfileSOCKSPort(t *testing.T) {
	base := Config{ListenAddress: "127.0.0.1:1080"}
	if got := runtimeConfig(base, profile.Profile{}).ListenAddress; got != base.ListenAddress {
		t.Fatalf("default listen address = %q", got)
	}
	if got := runtimeConfig(base, profile.Profile{SOCKSPort: 2080}).ListenAddress; got != "127.0.0.1:2080" {
		t.Fatalf("profile listen address = %q", got)
	}
}

func TestManagerOpenTrafficStreamRequiresMatchingActiveRuntimeSession(t *testing.T) {
	session := remote.Session{ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: "active", Generation: 1}
	runtime := &Runtime{ctx: context.Background(), status: Status{
		State: "connected", SessionID: session.ID, SessionGeneration: session.Generation,
	}}
	entry := &managedRuntime{session: session, runtime: runtime}
	manager := &Manager{active: map[string]*managedRuntime{"server": entry}}

	if _, err := manager.OpenTrafficStream(nil, "server", tunnel.TrafficModeExchange, "task"); err == nil {
		t.Fatal("nil Traffic stream context was accepted")
	}
	if _, err := manager.OpenTrafficStream(t.Context(), "other", tunnel.TrafficModeExchange, "task"); err == nil {
		t.Fatal("missing Data Plane runtime was accepted")
	}
	entry.recovering = true
	if _, err := manager.OpenTrafficStream(t.Context(), "server", tunnel.TrafficModeExchange, "task"); err == nil {
		t.Fatal("recovering Data Plane runtime was accepted")
	}
	entry.recovering = false
	runtime.status.SessionGeneration++
	if _, err := manager.OpenTrafficStream(t.Context(), "server", tunnel.TrafficModeExchange, "task"); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched Data Plane Session error = %v", err)
	}
	runtime.status.SessionGeneration = session.Generation
	if _, err := manager.OpenTrafficStream(t.Context(), "server", tunnel.TrafficModeExchange, "task"); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("missing current transport error = %v", err)
	}
}

func TestManagerReusesSessionAndReplacesChangedSession(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.42.0.0/16"}, PodIPs: []string{"10.43.7.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	starts := 0
	tunStarter := &testTUNStarter{}
	config := Config{
		TUNStarter: tunStarter,
		startForwarder: func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := clientConfig.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			starts++
			go acceptTestControl(listener)
			return &testForwarder{Listener: listener}, nil
		},
		listenSOCKS: func(_ context.Context, _, _ string, _ tunnel.SessionToken) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:" + strconv.Itoa(43000+starts))}, nil
		},
	}
	tickets := &testTickets{}
	manager, err := NewManager(tickets, config)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{
		ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel",
		DNSNamespace: "dns-scope", HostAliases: []profile.HostAlias{{Domain: "api.example.test", IP: "10.0.0.8"}},
	}
	first := remote.Session{ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: "active", Generation: 1, NetworkSpec: spec, NetworkSpecHash: hash}
	tickets.session = first
	firstStatus, err := manager.Connect(context.Background(), serverProfile, first)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := manager.Connect(context.Background(), serverProfile, first)
	if err != nil {
		t.Fatal(err)
	}
	if reused.SOCKSAddress != firstStatus.SOCKSAddress || starts != 1 {
		t.Fatalf("reused = %#v, starts = %d", reused, starts)
	}
	tunStatus, err := manager.StartTUN(context.Background(), serverProfile.ID)
	if err != nil {
		t.Fatal(err)
	}
	tunStarts, tunNetwork, tunBridge, tunNamespace, core := tunStarter.snapshot()
	if tunStatus.Mode != "tun" || tunStarts != 1 || tunBridge != firstStatus.SOCKSAddress ||
		tunNamespace != "dns-scope" || len(tunStarter.hosts) != 1 || tunStarter.hosts[0].Domain != "api.example.test" ||
		len(tunNetwork.PodCIDRs) != 1 ||
		len(tunNetwork.PodIPs) != 1 || tunNetwork.PodIPs[0] != "10.43.7.9" {
		t.Fatalf("TUN status = %#v, starter = %#v", tunStatus, tunStarter)
	}
	metrics, err := manager.Metrics(context.Background(), serverProfile.ID)
	if err != nil || metrics.ActiveConnections != 2 {
		t.Fatalf("metrics = %#v, %v", metrics, err)
	}
	logs, err := manager.Logs(context.Background(), serverProfile.ID)
	if err != nil || len(logs) != 2 || !strings.Contains(logs[0], "[SOCKS] listening on ") || logs[1] != "[TUN] ready" {
		t.Fatalf("logs = %#v, %v", logs, err)
	}
	if err := manager.UpdateDNSNamespace(context.Background(), serverProfile.ID, "observability"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateHostAliases(context.Background(), serverProfile.ID, []singbox.HostAlias{{Domain: "db.example.test", IP: "10.0.0.9"}}); err != nil {
		t.Fatal(err)
	}
	core.mu.Lock()
	if core.dnsNamespace != "observability" || len(core.hosts) != 1 || core.hosts[0].Domain != "db.example.test" {
		t.Fatalf("runtime network settings = namespace=%q aliases=%#v", core.dnsNamespace, core.hosts)
	}
	core.mu.Unlock()
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatalf("TUN was not reused: error=%v", err)
	}
	tunStarts, _, _, _, _ = tunStarter.snapshot()
	if tunStarts != 1 {
		t.Fatalf("TUN was not reused: starts=%d", tunStarts)
	}
	socksStatus, err := manager.StopTUN(serverProfile.ID)
	if err != nil || socksStatus.Mode != "socks" {
		t.Fatalf("stop TUN = %#v, %v", socksStatus, err)
	}
	second := first
	second.ID = "be75e37d-4c2f-48f2-a6a3-3fe7ef01130d"
	secondStatus, err := manager.Connect(context.Background(), serverProfile, second)
	if err != nil {
		t.Fatal(err)
	}
	if secondStatus.SessionID != second.ID || secondStatus.SOCKSAddress == firstStatus.SOCKSAddress || starts != 2 {
		t.Fatalf("replacement = %#v, starts = %d", secondStatus, starts)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if tickets.calls != 2 {
		t.Fatalf("RelayTicket calls = %d", tickets.calls)
	}
}

func TestManagerDisconnectClosesRuntimeAndRemovesProfile(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	bridge := &testBridge{address: testAddress("127.0.0.1:46001")}
	tickets := &testTickets{}
	manager, err := NewManager(tickets, Config{
		startForwarder: func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := clientConfig.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			go acceptTestControl(listener)
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
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: "active",
		Generation: 1, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	tickets.session = session
	if _, err := manager.Connect(context.Background(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.active[serverProfile.ID].runtime
	manager.mu.Unlock()

	if err := manager.Disconnect(serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.Done():
	default:
		t.Fatal("disconnect did not close the Data Plane runtime")
	}
	if _, closed := bridge.snapshot(); !closed {
		t.Fatal("disconnect did not close the local SOCKS bridge")
	}
	manager.mu.Lock()
	_, active := manager.active[serverProfile.ID]
	manager.mu.Unlock()
	if active {
		t.Fatal("disconnected profile remained active")
	}
	if _, err := manager.Dialer(serverProfile.ID); err == nil {
		t.Fatal("disconnected profile still exposed a dialer")
	}
	if _, err := manager.Status(serverProfile.ID); err == nil {
		t.Fatal("disconnected profile still exposed a status")
	}
	if err := manager.Disconnect(serverProfile.ID); err != nil {
		t.Fatalf("repeated disconnect = %v", err)
	}
}

func TestManagerRecoversControlStreamWithFreshSessionGeneration(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: "active",
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
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
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
	defer secondControl.Close()

	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		entry := manager.active[serverProfile.ID]
		ready := entry != nil && !entry.recovering && entry.session.Generation == session.Generation+1 &&
			entry.runtime.Status().SOCKSAddress == first.SOCKSAddress && entry.runtime.Status().Mode == "tun"
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
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: "active",
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
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
	initial, err := manager.Connect(context.Background(), serverProfile, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, initialCore := tunStarter.snapshot()
	firstControl := receiveControl(t, controls)
	defer firstControl.Close()

	if resumed := manager.ResumeAll(); resumed != 1 {
		t.Fatalf("resumed Profiles = %d, want 1", resumed)
	}
	secondControl := receiveControl(t, controls)
	defer secondControl.Close()

	deadline := time.Now().Add(time.Second)
	for {
		status, statusErr := manager.Status(serverProfile.ID)
		if statusErr == nil && status.State == "connected" && status.Mode == "tun" &&
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
	if wake.Status.State != "reconnecting" || !wake.Retryable {
		t.Fatalf("system resume status event = %#v", wake)
	}
}

func TestManagerReconfiguresTUNForRefreshedNetworkSpec(t *testing.T) {
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
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: "active",
		Generation: 4, NetworkSpec: initialSpec, NetworkSpecHash: initialHash,
	}
	controls := make(chan net.Conn, 4)
	var starts atomic.Int32
	tunStarter := &testTUNStarter{}
	tickets := &testTickets{session: session}
	tickets.refresh = func(current remote.Session) (remote.Session, error) {
		current.Generation++
		current.NetworkSpec = refreshedSpec
		current.NetworkSpecHash = refreshedHash
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
			return &testBridge{address: testAddress("127.0.0.1:" + strconv.Itoa(47000+int(starts.Load())))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
	firstStatus, err := manager.Connect(context.Background(), serverProfile, session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartTUN(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, firstCore := tunStarter.snapshot()
	_ = receiveControl(t, controls).Close()
	secondControl := receiveControl(t, controls)
	defer secondControl.Close()

	deadline := time.Now().Add(time.Second)
	var recovered Status
	for {
		manager.mu.Lock()
		entry := manager.active[serverProfile.ID]
		ready := entry != nil && !entry.recovering && entry.session.Generation == session.Generation+1 &&
			entry.session.NetworkSpecHash == refreshedHash && entry.runtime.Status().Mode == "tun"
		if entry != nil {
			recovered = entry.runtime.Status()
		}
		manager.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Data Plane did not replace the Runtime for the refreshed NetworkSpec")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if recovered.SOCKSAddress != firstStatus.SOCKSAddress {
		t.Fatalf("refreshed NetworkSpec changed stable SOCKS endpoint from %q to %q", firstStatus.SOCKSAddress, recovered.SOCKSAddress)
	}
	if starts.Load() != 2 {
		t.Fatalf("transport starts = %d", starts.Load())
	}
	tunStarts, tunNetwork, tunBridge, _, recoveredCore := tunStarter.snapshot()
	if tunStarts != 2 || recoveredCore == firstCore || tunBridge != recovered.SOCKSAddress ||
		len(tunNetwork.PodIPs) != 1 || tunNetwork.PodIPs[0] != "10.42.7.9" || len(tunNetwork.ServiceIPs) != 0 {
		t.Fatalf("refreshed TUN = starts %d network %#v bridge %q core-reused %t", tunStarts, tunNetwork, tunBridge, recoveredCore == firstCore)
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
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: "active",
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
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:45001")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
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
			gatewayAddress, closed := bridge.snapshot()
			if gatewayAddress != "127.0.0.1:0" || !closed {
				t.Fatalf("exhausted recovery bridge state = gateway %q closed %t", gatewayAddress, closed)
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
	for terminal.Status.State != "error" {
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

func TestRecoveryFailureActionDistinguishesOperatorActions(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		reason    string
		retryable bool
	}{
		{name: "authentication", err: &remote.APIError{Status: 401}, reason: reasonAuthenticationRequired},
		{name: "access", err: &remote.APIError{Status: 403}, reason: reasonAccessDenied},
		{name: "session", err: &remote.APIError{Status: 404}, reason: reasonSessionExpired, retryable: true},
		{name: "network", err: context.DeadlineExceeded, reason: reasonNetworkUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason, retryable := recoveryFailureAction(test.err)
			if reason != test.reason || retryable != test.retryable {
				t.Fatalf("action = %q/%t, want %q/%t", reason, retryable, test.reason, test.retryable)
			}
		})
	}
}

func TestManagerStatusCallbackCannotBlockLifecycleEvents(t *testing.T) {
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	manager, err := NewManager(&testTickets{}, Config{OnStatus: func(StatusEvent) {
		select {
		case <-callbackEntered:
		default:
			close(callbackEntered)
		}
		<-releaseCallback
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(releaseCallback)
		_ = manager.Shutdown()
	}()
	manager.emit("service", Status{State: "connected"}, nil)
	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("status callback was not invoked")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for generation := uint64(1); generation <= 100; generation++ {
			manager.emit("service", Status{State: "connected", SessionGeneration: generation}, nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked status callback stalled lifecycle event publication")
	}
}

func acceptTestControl(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	header, err := tunnel.ReadSessionHeader(connection)
	if err != nil || header.Command != tunnel.CommandControl {
		return
	}
	if _, err := tunnel.ReadAuthorizedControlSpec(connection); err != nil {
		return
	}
	if err := tunnel.WriteStatus(connection, nil); err != nil {
		return
	}
	var buffer [1]byte
	_, _ = connection.Read(buffer[:])
}

func acceptTestControlWithSignal(listener net.Listener, controls chan<- net.Conn) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	header, err := tunnel.ReadSessionHeader(connection)
	if err != nil || header.Command != tunnel.CommandControl {
		_ = connection.Close()
		return
	}
	if _, err := tunnel.ReadAuthorizedControlSpec(connection); err != nil {
		_ = connection.Close()
		return
	}
	if err := tunnel.WriteStatus(connection, nil); err != nil {
		_ = connection.Close()
		return
	}
	controls <- connection
}

func receiveControl(t *testing.T, controls <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case connection := <-controls:
		return connection
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Data Plane control stream")
		return nil
	}
}

package dataplane

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func TestManagerReusesSessionAndReplacesChangedSession(t *testing.T) {
	fixture := newManagerLifecycleFixture(t)
	firstStatus := fixture.connectAndAssertReuse(t)
	core := fixture.startAndAssertTUN(t, firstStatus)
	fixture.updateAndAssertNetworkSettings(t, core)
	fixture.stopAndReplace(t, firstStatus)
	fixture.shutdownAndAssertTickets(t)
}

type managerLifecycleFixture struct {
	manager    *Manager
	tickets    *testTickets
	tunStarter *testTUNStarter
	profile    profile.Profile
	first      remote.Session
	starts     int
}

func newManagerLifecycleFixture(t *testing.T) *managerLifecycleFixture {
	t.Helper()
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.42.0.0/16"}, PodIPs: []string{"10.43.7.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	fixture := &managerLifecycleFixture{
		tickets: &testTickets{}, tunStarter: &testTUNStarter{},
		profile: profile.Profile{
			ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath,
			DNSNamespace: "dns-scope",
			HostAliases:  []profile.HostAlias{{Domain: "api.example.test", IP: "10.0.0.8"}},
		},
		first: remote.Session{
			ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments",
			State: dataplaneSessionActive, Generation: 1, NetworkSpec: spec, NetworkSpecHash: hash,
		},
	}
	config := Config{
		TUNStarter: fixture.tunStarter,
		startForwarder: func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := clientConfig.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			fixture.starts++
			go acceptTestControl(listener)
			return &testForwarder{Listener: listener}, nil
		},
		listenSOCKS: func(context.Context, string) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:" + strconv.Itoa(43000+fixture.starts))}, nil
		},
	}
	fixture.manager, err = NewManager(fixture.tickets, config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.tickets.session = fixture.first
	return fixture
}

func (fixture *managerLifecycleFixture) connectAndAssertReuse(t *testing.T) Status {
	t.Helper()
	firstStatus, err := fixture.manager.Connect(context.Background(), fixture.profile, fixture.first)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := fixture.manager.Connect(context.Background(), fixture.profile, fixture.first)
	if err != nil {
		t.Fatal(err)
	}
	if reused.SOCKSAddress != firstStatus.SOCKSAddress || fixture.starts != 1 {
		t.Fatalf("reused = %#v, starts = %d", reused, fixture.starts)
	}
	return firstStatus
}

func (fixture *managerLifecycleFixture) startAndAssertTUN(t *testing.T, firstStatus Status) *testCore {
	t.Helper()
	tunStatus, err := fixture.manager.StartTUN(context.Background(), fixture.profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	tunStarts, tunNetwork, tunBridge, tunNamespace, core := fixture.tunStarter.snapshot()
	if tunStatus.Mode != ModeTUN || tunStarts != 1 || tunBridge != firstStatus.SOCKSAddress ||
		tunNamespace != "dns-scope" || len(fixture.tunStarter.hosts) != 1 ||
		fixture.tunStarter.hosts[0].Domain != "api.example.test" ||
		len(tunNetwork.PodCIDRs) != 1 ||
		len(tunNetwork.PodIPs) != 1 || tunNetwork.PodIPs[0] != "10.43.7.9" {
		t.Fatalf("TUN status = %#v, starter = %#v", tunStatus, fixture.tunStarter)
	}
	logs, err := fixture.manager.Logs(context.Background(), fixture.profile.ID)
	if err != nil || len(logs) < 2 || !strings.Contains(logs[0], "[SOCKS] listening on ") {
		t.Fatalf("logs = %#v, %v", logs, err)
	}
	foundTUNReady := false
	for _, line := range logs {
		if line == "[TUN] ready" {
			foundTUNReady = true
		}
	}
	if !foundTUNReady {
		t.Fatalf("logs = %#v, want [TUN] ready", logs)
	}
	return core
}

func (fixture *managerLifecycleFixture) updateAndAssertNetworkSettings(t *testing.T, core *testCore) {
	t.Helper()
	if err := fixture.manager.UpdateDNSNamespace(
		context.Background(), fixture.profile.ID, "observability",
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.UpdateHostAliases(
		context.Background(),
		fixture.profile.ID,
		[]sessionspec.HostAlias{{Domain: "db.example.test", IP: "10.0.0.9"}},
	); err != nil {
		t.Fatal(err)
	}
	core.mu.Lock()
	if core.dnsNamespace != "observability" || len(core.hosts) != 1 || core.hosts[0].Domain != "db.example.test" {
		t.Fatalf("runtime network settings = namespace=%q aliases=%#v", core.dnsNamespace, core.hosts)
	}
	core.mu.Unlock()
	if _, err := fixture.manager.StartTUN(context.Background(), fixture.profile.ID); err != nil {
		t.Fatalf("TUN was not reused: error=%v", err)
	}
	tunStarts, _, _, _, _ := fixture.tunStarter.snapshot()
	if tunStarts != 1 {
		t.Fatalf("TUN was not reused: starts=%d", tunStarts)
	}
}

func (fixture *managerLifecycleFixture) stopAndReplace(t *testing.T, firstStatus Status) {
	t.Helper()
	socksStatus, err := fixture.manager.StopTUN(fixture.profile.ID)
	if err != nil || socksStatus.Mode != ModeSOCKS {
		t.Fatalf("stop TUN = %#v, %v", socksStatus, err)
	}
	second := fixture.first
	second.ID = "be75e37d-4c2f-48f2-a6a3-3fe7ef01130d"
	secondStatus, err := fixture.manager.Connect(context.Background(), fixture.profile, second)
	if err != nil {
		t.Fatal(err)
	}
	if secondStatus.SessionID != second.ID || secondStatus.SOCKSAddress == firstStatus.SOCKSAddress ||
		fixture.starts != 2 {
		t.Fatalf("replacement = %#v, starts = %d", secondStatus, fixture.starts)
	}
}

func (fixture *managerLifecycleFixture) shutdownAndAssertTickets(t *testing.T) {
	t.Helper()
	if err := fixture.manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if fixture.tickets.calls != 2 {
		t.Fatalf("RelayTicket calls = %d", fixture.tickets.calls)
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
		listenSOCKS: func(context.Context, string) (localBridge, error) {
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
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", Namespace: "payments", State: dataplaneSessionActive,
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

func TestManagerRuntimeOutlivesConnectContext(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(&testTickets{}, Config{
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
		listenSOCKS: func(context.Context, string) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:49010")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	connectCtx, cancelConnect := context.WithCancel(context.Background())
	_, err = manager.Connect(
		connectCtx,
		profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath},
		remote.Session{
			ID: uuid.NewString(), Namespace: "development", State: dataplaneSessionActive,
			Generation: 1, NetworkSpec: spec, NetworkSpecHash: hash,
		},
	)
	if err != nil {
		cancelConnect()
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.active["service"].runtime
	manager.mu.Unlock()
	cancelConnect()
	select {
	case <-runtime.Done():
		t.Fatal("Data Plane Runtime inherited the completed Connect context")
	case <-time.After(100 * time.Millisecond):
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

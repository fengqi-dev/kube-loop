package dataplane

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
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
	updates chan remote.SessionUpdate
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

type blockingTUNStarter struct {
	*testTUNStarter

	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func (starter *blockingTUNStarter) Start(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress, namespace string,
	hosts []singbox.HostAlias,
) (singbox.RunningCore, error) {
	starter.startedOnce.Do(func() { close(starter.started) })
	select {
	case <-starter.release:
		return starter.testTUNStarter.Start(ctx, network, bridgeAddress, namespace, hosts)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (starter *blockingTUNStarter) unblock() {
	starter.releaseOnce.Do(func() { close(starter.release) })
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

func (tickets *testTickets) SessionUpdates() <-chan remote.SessionUpdate {
	return tickets.updates
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

func TestModeValidationAndInvalidSwitches(t *testing.T) {
	for _, mode := range []Mode{ModeSOCKS, ModeTUN} {
		if err := mode.Validate(); err != nil {
			t.Fatalf("mode %q validation error = %v", mode, err)
		}
	}
	invalid := Mode("invalid")
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid Data Plane mode was accepted")
	}
	manager := &Manager{}
	if _, err := manager.SwitchMode(t.Context(), "server", invalid); err == nil {
		t.Fatal("invalid SwitchMode was accepted")
	}
	if _, err := manager.ConnectMode(
		t.Context(),
		profile.Profile{ID: "server"},
		remote.Session{},
		invalid,
	); err == nil {
		t.Fatal("invalid ConnectMode was accepted")
	}
}

func TestManagerThinOperationsRequireConnectedRuntime(t *testing.T) {
	manager := &Manager{active: map[string]*managedRuntime{}}
	if _, err := manager.Metrics(t.Context(), "missing"); err == nil {
		t.Fatal("Metrics accepted a missing Runtime")
	}
	if err := manager.TestConnectivity(t.Context(), "missing"); err == nil {
		t.Fatal("TestConnectivity accepted a missing Runtime")
	}
	if _, err := manager.Logs(t.Context(), "missing"); err == nil {
		t.Fatal("Logs accepted a missing Runtime")
	}
	if _, err := manager.ConfigJSON("missing"); err == nil {
		t.Fatal("ConfigJSON accepted a missing Runtime")
	}
	if err := manager.UpdateDNSNamespace(t.Context(), "missing", "default"); err == nil {
		t.Fatal("UpdateDNSNamespace accepted a missing Runtime")
	}
	if err := manager.UpdateHostAliases(t.Context(), "missing", nil); err == nil {
		t.Fatal("UpdateHostAliases accepted a missing Runtime")
	}
	if _, err := manager.Dialer("missing"); err == nil {
		t.Fatal("Dialer accepted a missing Runtime")
	}
}

func TestManagerSetHostTCPHandlerUpdatesActiveRuntime(t *testing.T) {
	bridge := &testBridge{}
	runtime := &Runtime{bridge: bridge, status: Status{Mode: ModeTUN}}
	manager := &Manager{
		active:  map[string]*managedRuntime{"server": {runtime: runtime}},
		hostTCP: map[string]socksbridge.HostTCPHandler{},
	}
	handler := func(string, uint16) (func(net.Conn), bool) { return nil, false }
	if err := manager.SetHostTCPHandler(" server ", handler); err != nil {
		t.Fatal(err)
	}
	bridge.mu.Lock()
	installed := bridge.hostTCP != nil
	bridge.mu.Unlock()
	if !installed || manager.hostTCP["server"] == nil {
		t.Fatal("Host TCP handler was not installed on Manager and Runtime")
	}
	if err := manager.SetHostTCPHandler("missing", handler); err == nil {
		t.Fatal("Host TCP handler accepted a missing Runtime")
	}
}

func TestSlowTUNStartDoesNotBlockAnotherProfileStatus(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	starter := &blockingTUNStarter{
		testTUNStarter: &testTUNStarter{},
		started:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	t.Cleanup(starter.unblock)
	var starts atomic.Int32
	manager, err := NewManager(&testTickets{}, Config{
		TUNStarter: starter,
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
			port := 49000 + starts.Add(1)
			return &testBridge{address: testAddress("127.0.0.1:" + strconv.Itoa(int(port)))}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	profiles := []profile.Profile{
		{ID: "service-slow", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath},
		{ID: "service-independent", BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath},
	}
	for index, serverProfile := range profiles {
		session := remote.Session{
			ID: uuid.NewString(), Namespace: "development", State: dataplaneSessionActive,
			Generation: uint64(index + 1), NetworkSpec: spec, NetworkSpecHash: hash,
		}
		if _, err := manager.Connect(context.Background(), serverProfile, session); err != nil {
			t.Fatal(err)
		}
	}
	tunResult := make(chan error, 1)
	go func() {
		_, startErr := manager.StartTUN(context.Background(), profiles[0].ID)
		tunResult <- startErr
	}()
	select {
	case <-starter.started:
	case <-time.After(time.Second):
		t.Fatal("TUN start did not begin")
	}
	statusResult := make(chan error, 1)
	go func() {
		_, statusErr := manager.Status(profiles[1].ID)
		statusResult <- statusErr
	}()
	select {
	case err := <-statusResult:
		if err != nil {
			t.Fatalf("independent Status failed: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("independent Status blocked on another profile's TUN startup")
	}
	starter.unblock()
	if err := <-tunResult; err != nil {
		t.Fatal(err)
	}
}

func TestManagerShutdownWaitsForStatusCallbackAndRejectsConnect(t *testing.T) {
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	var callbackOnce sync.Once
	manager, err := NewManager(&testTickets{}, Config{OnStatus: func(StatusEvent) {
		callbackOnce.Do(func() { close(callbackStarted) })
		<-releaseCallback
	}})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCallback) }) }
	t.Cleanup(release)
	manager.emit("server", Status{State: dataplaneConnected}, nil)
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("Data Plane status callback did not start")
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- manager.Shutdown() }()
	select {
	case err := <-shutdown:
		t.Fatalf("Shutdown returned before status callback completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-shutdown; err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(
		t.Context(),
		profile.Profile{ID: "server"},
		remote.Session{State: dataplaneSessionActive},
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("Connect after Shutdown error = %v, want ErrClosed", err)
	}
}

func TestManagerOpenTrafficStreamRequiresMatchingActiveRuntimeSession(t *testing.T) {
	session := remote.Session{ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: dataplaneSessionActive, Generation: 1}
	runtime := &Runtime{ctx: context.Background(), status: Status{
		State: dataplaneConnected, SessionID: session.ID, SessionGeneration: session.Generation,
	}}
	entry := &managedRuntime{session: session, runtime: runtime}
	manager := &Manager{active: map[string]*managedRuntime{"server": entry}}

	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
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
		listenSOCKS: func(_ context.Context, _, _ string, _ tunnel.SessionToken) (localBridge, error) {
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
	metrics, err := fixture.manager.Metrics(context.Background(), fixture.profile.ID)
	if err != nil || metrics.ActiveConnections != 2 {
		t.Fatalf("metrics = %#v, %v", metrics, err)
	}
	logs, err := fixture.manager.Logs(context.Background(), fixture.profile.ID)
	if err != nil || len(logs) != 2 || !strings.Contains(logs[0], "[SOCKS] listening on ") || logs[1] != "[TUN] ready" {
		t.Fatalf("logs = %#v, %v", logs, err)
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
		[]singbox.HostAlias{{Domain: "db.example.test", IP: "10.0.0.9"}},
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
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
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
		listenSOCKS: func(_ context.Context, _, _ string, _ tunnel.SessionToken) (localBridge, error) {
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
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
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

func acceptTestControl(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() {
		_ = connection.Close() // The background accept helper closes only to release the test connection.
	}()
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

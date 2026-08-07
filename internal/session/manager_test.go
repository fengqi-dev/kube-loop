package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type fakeProvider struct {
	discovery         cluster.Discovery
	namespaces        []string
	err               error
	forwarder         *fakeForwarder
	probeCapabilities func(context.Context, string) (cluster.Capabilities, error)
}

func (f *fakeProvider) Contexts() ([]cluster.ContextInfo, error) { return nil, nil }
func (f *fakeProvider) ServerVersion(context.Context, string) (string, error) {
	return "v1.29.0-fake", nil
}
func (f *fakeProvider) Namespaces(context.Context, string) ([]string, error) {
	if f.namespaces != nil {
		return f.namespaces, nil
	}
	return []string{"default"}, nil
}
func (f *fakeProvider) ListServices(
	context.Context, string, string,
) ([]cluster.ServiceInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []cluster.ServiceInfo{{
		Name: "api", Namespace: "default", ClusterIP: "10.96.1.1",
		Ports: []cluster.ServicePortInfo{{Name: "http", Port: 80, Protocol: "TCP"}},
	}}, nil
}
func (f *fakeProvider) ListPods(
	context.Context, string, string,
) ([]cluster.PodInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []cluster.PodInfo{{
		Name: "api-0", Namespace: "default", Phase: "Running", Ready: true,
		Ports: []cluster.PodPortInfo{{Name: "http", Port: 8080, Protocol: "TCP"}},
	}}, nil
}
func (f *fakeProvider) StartPodPortForward(
	context.Context, string, string, string, uint16, uint16,
) (cluster.PortForward, error) {
	return f.StartPortForward(context.Background(), "", "", 0)
}
func (f *fakeProvider) ResolveServiceBackend(
	context.Context, string, string, string, int32,
) (string, uint16, error) {
	if f.err != nil {
		return "", 0, f.err
	}
	return "api-0", 8080, nil
}
func (f *fakeProvider) Discover(context.Context, string, []string) (cluster.Discovery, error) {
	return f.discovery, f.err
}
func (f *fakeProvider) WatchInventory(
	context.Context, string, []string, func(cluster.InventorySnapshot),
) (io.Closer, error) {
	return closerFunc(func() {}), f.err
}
func (f *fakeProvider) ProbeCapabilities(ctx context.Context, contextName string) (cluster.Capabilities, error) {
	if f.probeCapabilities != nil {
		return f.probeCapabilities(ctx, contextName)
	}
	return cluster.Capabilities{
		GatewayInstall: true, GatewayPortForward: true, ClusterNodes: true, InventoryCluster: true,
		ServiceWrite: true, ServiceCreate: true,
	}, nil
}
func (f *fakeProvider) GetGateway(context.Context, string) (cluster.GatewayInfo, error) {
	if f.err != nil {
		return cluster.GatewayInfo{}, f.err
	}
	return cluster.GatewayInfo{Name: "gateway-pod", IP: "10.244.0.8"}, nil
}
func (f *fakeProvider) EnsureGateway(context.Context, string, string) (cluster.GatewayInfo, error) {
	if f.err != nil {
		return cluster.GatewayInfo{}, f.err
	}
	return cluster.GatewayInfo{Name: "gateway-pod", IP: "10.244.0.8"}, nil
}
func (f *fakeProvider) StartPortForward(
	context.Context, string, string, uint16,
) (cluster.PortForward, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.forwarder == nil {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
		go func() { _ = server.Serve(listener) }()
		f.forwarder = &fakeForwarder{address: listener.Addr().String(), listener: listener}
	}
	return f.forwarder, nil
}
func (f *fakeProvider) ApplyServiceIntercept(
	context.Context, string, *cluster.ServiceInterceptSnapshot, string,
) error {
	return f.err
}
func (f *fakeProvider) RestoreServiceIntercept(
	context.Context, string, cluster.ServiceInterceptSnapshot,
) error {
	return f.err
}
func (f *fakeProvider) CreatePreviewService(
	context.Context, string, cluster.PreviewServiceSnapshot, string,
) (*corev1.Service, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: "10.96.9.9"}}, nil
}
func (f *fakeProvider) DeletePreviewService(
	context.Context, string, cluster.PreviewServiceSnapshot,
) error {
	return f.err
}
func (f *fakeProvider) GetService(
	context.Context, string, string, string,
) (*corev1.Service, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &corev1.Service{
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.1.1",
			Selector:  map[string]string{"app": "api"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
			}},
		},
	}, nil
}

type fakeForwarder struct {
	mu       sync.Mutex
	closed   bool
	address  string
	listener net.Listener
}

func (f *fakeForwarder) Address() string { return f.address }
func (f *fakeForwarder) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	if f.listener != nil {
		_ = f.listener.Close()
	}
	return nil
}

type fakeCore struct {
	process *fakeProcess
	started chan struct{}
	network singbox.NetworkSpec
}

func newFakeCore() *fakeCore {
	return &fakeCore{process: &fakeProcess{done: make(chan struct{})}, started: make(chan struct{})}
}

func (f *fakeCore) Start(
	_ context.Context, network singbox.NetworkSpec, _ string, _ string, _ []singbox.HostAlias,
) (singbox.RunningCore, error) {
	f.network = network
	close(f.started)
	return f.process, nil
}

type fakeProcess struct {
	once         sync.Once
	done         chan struct{}
	err          error
	updatedHosts []singbox.HostAlias
}

func (f *fakeProcess) Done() <-chan struct{} { return f.done }
func (f *fakeProcess) Err() error            { return f.err }
func (f *fakeProcess) Snapshot(context.Context) (singbox.Metrics, error) {
	return singbox.Metrics{Connections: []singbox.Connection{}}, nil
}
func (f *fakeProcess) TrafficEndpoints() singbox.TrafficEndpoints {
	endpoint := func(username string) singbox.TrafficEndpoint {
		return singbox.TrafficEndpoint{
			Address: "127.0.0.1:18080", Username: username, Password: "test-password",
		}
	}
	return singbox.TrafficEndpoints{
		PortForward:  endpoint(singbox.TrafficUserPortForward),
		Exchange:     endpoint(singbox.TrafficUserExchange),
		Preview:      endpoint(singbox.TrafficUserPreview),
		MirrorShadow: endpoint(singbox.TrafficUserMirrorShadow),
	}
}
func (f *fakeProcess) Config() []byte {
	return []byte(`{"log":{"level":"info"}}`)
}
func (f *fakeProcess) SessionID() string { return "session-test123" }
func (f *fakeProcess) ReadLogs(context.Context) ([]string, error) {
	return nil, nil
}
func (f *fakeProcess) UpdateDNSNamespace(context.Context, string) error { return nil }
func (f *fakeProcess) UpdateHostAliases(_ context.Context, hosts []singbox.HostAlias) error {
	f.updatedHosts = append([]singbox.HostAlias(nil), hosts...)
	return nil
}
func (f *fakeProcess) ProbeClusterDNS(context.Context) error { return nil }
func (f *fakeProcess) DNSPort() int                          { return 1053 }
func (f *fakeProcess) InternalDNSPort() int                  { return 1054 }
func (f *fakeProcess) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

func testBridge(context.Context, string, string) (net.Listener, error) {
	return &fakeListener{}, nil
}

type fakeListener struct {
	address string
}

func (f *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (f *fakeListener) Close() error              { return nil }
func (f *fakeListener) Addr() net.Addr {
	if f.address != "" {
		return fakeAddress(f.address)
	}
	return fakeAddress("127.0.0.1:23456")
}

type fakeAddress string

func (f fakeAddress) Network() string { return "tcp" }
func (f fakeAddress) String() string  { return string(f) }

type trackedListener struct {
	mu     sync.Mutex
	closed bool
}

func (l *trackedListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *trackedListener) Addr() net.Addr            { return fakeAddress("127.0.0.1:23457") }
func (l *trackedListener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return nil
}

func (l *trackedListener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

type failingCore struct {
	err error
}

func (f failingCore) Start(
	context.Context, singbox.NetworkSpec, string, string, []singbox.HostAlias,
) (singbox.RunningCore, error) {
	return nil, f.err
}

func TestManagerPublishesConnectedStateAndCleansUp(t *testing.T) {
	want := cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}
	provider := &fakeProvider{
		discovery:  want,
		namespaces: []string{"default", "payments", "platform"},
	}
	core := newFakeCore()
	manager := NewManager(
		provider, WithCore(core), WithBridgeFactory(testBridge), WithGatewayImage("gateway:test"),
	)
	connected := make(chan State, 1)
	idleResourcesClosed := make(chan bool, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseConnected {
			connected <- state
		}
		if state.Phase == PhaseIdle && provider.forwarder != nil {
			provider.forwarder.mu.Lock()
			closed := provider.forwarder.closed
			provider.forwarder.mu.Unlock()
			select {
			case idleResourcesClosed <- closed:
			default:
			}
		}
	})
	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err != nil {
		t.Fatal(err)
	}
	var state State
	select {
	case state = <-connected:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for connected state; current state: %#v", manager.State())
	}
	if state.Discovery == nil || len(state.Discovery.PodCIDRs) != 1 {
		t.Fatalf("unexpected discovery: %#v", state.Discovery)
	}
	if state.ConnectedAt == nil {
		t.Fatal("connected state does not include connection time")
	}
	if state.Mode != ConnectionModeTUN {
		t.Fatalf("mode = %q, want %q", state.Mode, ConnectionModeTUN)
	}
	if state.SOCKSPort != 0 {
		t.Fatalf("SOCKSPort = %d, want 0 in TUN mode", state.SOCKSPort)
	}
	if got := strings.Join(core.network.Namespaces, ","); got != "default,payments,platform" {
		t.Fatalf("sing-box namespaces = %q", got)
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	provider.forwarder.mu.Lock()
	closed := provider.forwarder.closed
	provider.forwarder.mu.Unlock()
	if !closed {
		t.Fatal("port-forward was not closed")
	}
	if manager.State().Phase != PhaseIdle {
		t.Fatalf("unexpected state after disconnect: %s", manager.State().Phase)
	}
	select {
	case closedAtIdle := <-idleResourcesClosed:
		if !closedAtIdle {
			t.Fatal("idle state was published before runtime resources were closed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for idle state")
	}
}

func TestManagerSOCKSModeSkipsTUNCore(t *testing.T) {
	provider := &fakeProvider{discovery: cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}}
	var listenAddresses []string
	manager := NewManager(
		provider,
		WithCore(failingCore{err: errors.New("TUN core must not start")}),
		WithBridgeFactory(func(
			_ context.Context, _, listenAddress string,
		) (net.Listener, error) {
			listenAddresses = append(listenAddresses, listenAddress)
			return &fakeListener{address: listenAddress}, nil
		}),
	)
	connected := make(chan State, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseConnected {
			connected <- state
		}
	})

	if err := manager.Connect(context.Background(), Request{
		Context: "dev",
		Mode:    ConnectionModeSOCKS,
	}); err != nil {
		t.Fatal(err)
	}
	state := receiveState(t, connected)
	if state.Mode != ConnectionModeSOCKS {
		t.Fatalf("mode = %q, want %q", state.Mode, ConnectionModeSOCKS)
	}
	if state.SOCKSPort != DefaultSOCKSPort {
		t.Fatalf("SOCKSPort = %d, want %d", state.SOCKSPort, DefaultSOCKSPort)
	}
	wantListenAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultSOCKSPort))
	if fmt.Sprint(listenAddresses) != fmt.Sprint([]string{wantListenAddress}) {
		t.Fatalf("listen addresses = %v, want [%s]", listenAddresses, wantListenAddress)
	}
	if state.Network == nil || state.Network.RoutingMode != "proxy" {
		t.Fatalf("unexpected SOCKS network diagnostics: %#v", state.Network)
	}
	manager.mu.RLock()
	runningCore := manager.runningCore
	manager.mu.RUnlock()
	if runningCore != nil {
		t.Fatal("SOCKS mode started the TUN core")
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSOCKSModeFallsBackWhenDefaultPortIsBusy(t *testing.T) {
	provider := &fakeProvider{discovery: cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}}
	var listenAddresses []string
	manager := NewManager(
		provider,
		WithCore(failingCore{err: errors.New("TUN core must not start")}),
		WithBridgeFactory(func(
			_ context.Context, _, listenAddress string,
		) (net.Listener, error) {
			listenAddresses = append(listenAddresses, listenAddress)
			if len(listenAddresses) == 1 {
				return nil, errors.New("address already in use")
			}
			return &fakeListener{}, nil
		}),
	)
	connected := make(chan State, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseConnected {
			connected <- state
		}
	})

	if err := manager.Connect(context.Background(), Request{
		Context: "dev",
		Mode:    ConnectionModeSOCKS,
	}); err != nil {
		t.Fatal(err)
	}
	state := receiveState(t, connected)
	if state.SOCKSPort != 23456 {
		t.Fatalf("fallback SOCKSPort = %d, want 23456", state.SOCKSPort)
	}
	wantAddresses := []string{
		net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultSOCKSPort)),
		"127.0.0.1:0",
	}
	if fmt.Sprint(listenAddresses) != fmt.Sprint(wantAddresses) {
		t.Fatalf("listen addresses = %v, want %v", listenAddresses, wantAddresses)
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerCancelDuringStartupPublishesIdleAndAllowsReconnect(t *testing.T) {
	probeStarted := make(chan struct{})
	provider := &fakeProvider{
		probeCapabilities: func(ctx context.Context, _ string) (cluster.Capabilities, error) {
			close(probeStarted)
			<-ctx.Done()
			return cluster.Capabilities{}, ctx.Err()
		},
	}
	manager := NewManager(
		provider, WithCore(newFakeCore()), WithBridgeFactory(testBridge),
	)
	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-probeStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for startup probe")
	}

	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	state := manager.State()
	if state.Phase != PhaseIdle {
		t.Fatalf("phase after startup cancellation = %s, want %s", state.Phase, PhaseIdle)
	}
	if state.Context != "dev" {
		t.Fatalf("context after startup cancellation = %q, want dev", state.Context)
	}

	provider.probeCapabilities = nil
	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err != nil {
		t.Fatalf("reconnect after startup cancellation: %v", err)
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerPublishesGatewayError(t *testing.T) {
	manager := NewManager(
		&fakeProvider{err: errors.New("forbidden")},
		WithCore(newFakeCore()),
		WithBridgeFactory(testBridge),
	)
	failed := make(chan State, 1)
	manager.Subscribe(func(state State) {
		if state.Phase == PhaseError {
			failed <- state
		}
	})
	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err != nil {
		t.Fatal(err)
	}
	state := receiveState(t, failed)
	if state.Error != "forbidden" || state.Message != "Could not install the cluster Gateway" {
		t.Fatalf("unexpected error state: %#v", state)
	}
}

func TestManagerCoreFailurePublishesPhasesAndRollsBackResources(t *testing.T) {
	provider := &fakeProvider{discovery: cluster.Discovery{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
	}}
	bridge := &trackedListener{}
	manager := NewManager(
		provider,
		WithCore(failingCore{err: errors.New("core failed")}),
		WithBridgeFactory(func(context.Context, string, string) (net.Listener, error) {
			return bridge, nil
		}),
	)

	var phasesMu sync.Mutex
	var phases []Phase
	failed := make(chan State, 1)
	manager.Subscribe(func(state State) {
		phasesMu.Lock()
		phases = append(phases, state.Phase)
		phasesMu.Unlock()
		if state.Phase == PhaseError {
			failed <- state
		}
	})

	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err != nil {
		t.Fatal(err)
	}
	state := receiveState(t, failed)
	if state.Message != "Could not start sing-box TUN" || state.Error != "core failed" {
		t.Fatalf("unexpected error state: %#v", state)
	}

	waitFor(t, func() bool {
		if provider.forwarder == nil || !bridge.isClosed() {
			return false
		}
		provider.forwarder.mu.Lock()
		defer provider.forwarder.mu.Unlock()
		return provider.forwarder.closed
	})

	phasesMu.Lock()
	gotPhases := compactPhases(phases)
	phasesMu.Unlock()
	wantPhases := []Phase{
		PhaseChecking,
		PhaseInstalling,
		PhaseDiscovering,
		PhaseStarting,
		PhaseError,
	}
	if fmt.Sprint(gotPhases) != fmt.Sprint(wantPhases) {
		t.Fatalf("phases = %v, want %v", gotPhases, wantPhases)
	}
}

func compactPhases(phases []Phase) []Phase {
	compacted := make([]Phase, 0, len(phases))
	for _, phase := range phases {
		if len(compacted) == 0 || compacted[len(compacted)-1] != phase {
			compacted = append(compacted, phase)
		}
	}
	return compacted
}

func TestStateJSONContract(t *testing.T) {
	state := State{
		Phase:             PhaseConnected,
		Context:           "dev",
		Namespace:         "default",
		DNSNamespace:      "team-a",
		Message:           "Connected",
		Error:             "sample",
		DNSWarning:        "warning",
		InventoryRevision: 7,
		KubernetesVersion: "v1.35.1",
		UpdatedAt:         time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		ScopeNamespaces:   nil,
		GatewayManifest:   "",
		CoreVersion:       "",
		ConnectedAt:       nil,
		Discovery:         nil,
		Capabilities:      nil,
		Network:           nil,
		Pods:              nil,
		Services:          nil,
		Events:            nil,
		Metrics:           nil,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"phase":"connected","context":"dev","namespace":"default","dnsNamespace":"team-a","message":"Connected","error":"sample","dnsWarning":"warning","inventoryRevision":7,"kubernetesVersion":"v1.35.1","updatedAt":"2026-08-01T12:00:00Z"}`
	if string(data) != want {
		t.Fatalf("State JSON changed:\n got: %s\nwant: %s", data, want)
	}
}

func TestManagerRejectsSecondConnection(t *testing.T) {
	manager := NewManager(
		&fakeProvider{discovery: cluster.Discovery{
			PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.1"},
		}},
		WithCore(newFakeCore()),
		WithBridgeFactory(testBridge),
	)
	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background(), Request{Context: "dev"}); err == nil {
		t.Fatal("expected second connection to be rejected")
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func receiveState(t *testing.T, states <-chan State) State {
	t.Helper()
	select {
	case state := <-states:
		return state
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for session state")
		return State{}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestRetainMetricsKeepsRecentConnections(t *testing.T) {
	manager := NewManager(&fakeProvider{})
	first := manager.retainMetrics(singbox.Metrics{
		DownloadTotal: 100,
		Connections: []singbox.Connection{{
			ID:          "conn-1",
			Network:     "tcp",
			Destination: "10.96.0.1:443",
			Download:    50,
		}},
	})
	if len(first.Connections) != 1 {
		t.Fatalf("expected 1 live connection, got %d", len(first.Connections))
	}

	retained := manager.retainMetrics(singbox.Metrics{
		DownloadTotal: 200,
		Connections:   nil,
	})
	if retained.DownloadTotal != 200 {
		t.Fatalf("download total = %d, want 200", retained.DownloadTotal)
	}
	if len(retained.Connections) != 1 || retained.Connections[0].ID != "conn-1" {
		t.Fatalf("expected retained connection, got %#v", retained.Connections)
	}

	manager.clearRecentConnections()
	cleared := manager.retainMetrics(singbox.Metrics{Connections: nil})
	if len(cleared.Connections) != 0 {
		t.Fatalf("expected no connections after clear, got %#v", cleared.Connections)
	}
}

func TestResolveGatewayImage(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_IMAGE", "")
	if got := ResolveGatewayImage("dev"); got != DefaultGatewayImage {
		t.Fatalf("dev = %q, want %q", got, DefaultGatewayImage)
	}
	if got := ResolveGatewayImage(
		"dev",
		"kube-loop-gateway:dev-deadbeef",
	); got != "kube-loop-gateway:dev-deadbeef" {
		t.Fatalf("wails dev = %q", got)
	}
	if got := ResolveGatewayImage("v0.2.0"); got != "ghcr.io/fengqi-dev/kube-loop/gateway:v0.2.0" {
		t.Fatalf("release = %q", got)
	}
	t.Setenv("KUBELOOP_GATEWAY_IMAGE", "registry.example/gateway:custom")
	if got := ResolveGatewayImage(
		"v0.2.0",
		"kube-loop-gateway:dev-deadbeef",
	); got != "registry.example/gateway:custom" {
		t.Fatalf("env override = %q", got)
	}
}

func TestRetainMetricsCapsPublishedConnections(t *testing.T) {
	manager := NewManager(&fakeProvider{})
	connections := make([]singbox.Connection, 250)
	for i := range connections {
		connections[i] = singbox.Connection{
			ID:          fmt.Sprintf("conn-%d", i),
			Network:     "tcp",
			Destination: "10.96.0.1:80",
			Download:    int64(i),
		}
	}
	metrics := manager.retainMetrics(singbox.Metrics{Connections: connections})
	if len(metrics.Connections) != maxPublishedConnections {
		t.Fatalf("published = %d, want %d", len(metrics.Connections), maxPublishedConnections)
	}
	if metrics.Connections[0].Download < metrics.Connections[len(metrics.Connections)-1].Download {
		t.Fatalf("expected higher-traffic connections first, got first=%d last=%d",
			metrics.Connections[0].Download, metrics.Connections[len(metrics.Connections)-1].Download)
	}
}

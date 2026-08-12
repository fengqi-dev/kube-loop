package dataplane

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/websocketmux"
	"github.com/google/uuid"
)

type testForwarder struct {
	net.Listener
	closeErr   error
	closeCalls int
}

func (forwarder *testForwarder) Address() string { return forwarder.Listener.Addr().String() }
func (forwarder *testForwarder) Close() error {
	forwarder.closeCalls++
	_ = forwarder.Listener.Close()
	return forwarder.closeErr
}

type testBridge struct {
	mu         sync.Mutex
	address    net.Addr
	closed     bool
	closeErr   error
	closeCalls int
	gateway    string
	token      tunnel.SessionToken
	hostTCP    socksbridge.HostTCPHandler
}

func (bridge *testBridge) Addr() net.Addr { return bridge.address }
func (bridge *testBridge) Close() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.closed = true
	bridge.closeCalls++
	return bridge.closeErr
}
func (bridge *testBridge) SetGatewayAddress(address string) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.gateway = address
}
func (bridge *testBridge) SetGateway(address string, token tunnel.SessionToken) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.gateway = address
	bridge.token = token
}
func (bridge *testBridge) SetHostTCPHandler(handler socksbridge.HostTCPHandler) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.hostTCP = handler
}
func (bridge *testBridge) snapshot() (string, bool) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.gateway, bridge.closed
}
func (bridge *testBridge) transport() (string, tunnel.SessionToken) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.gateway, bridge.token
}

type testAddress string

func (address testAddress) Network() string { return "tcp" }
func (address testAddress) String() string  { return string(address) }

type testCloseConn struct {
	net.Conn
	closeErr   error
	closeCalls int
}

func (connection *testCloseConn) Close() error {
	connection.closeCalls++
	_ = connection.Conn.Close()
	return connection.closeErr
}

type readSignalConn struct {
	net.Conn
	once sync.Once
	read chan struct{}
}

func (connection *readSignalConn) Read(buffer []byte) (int, error) {
	count, err := connection.Conn.Read(buffer)
	if count > 0 {
		connection.once.Do(func() { close(connection.read) })
	}
	return count, err
}

func TestStartRegistersAuthorizedControlBeforeOpeningSOCKS(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.42.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwarder := &testForwarder{Listener: listener}
	statusRead := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		header, readErr := tunnel.ReadSessionHeader(connection)
		if readErr != nil || header.Command != tunnel.CommandControl {
			return
		}
		registered, readErr := tunnel.ReadAuthorizedControlSpec(connection)
		if readErr != nil {
			return
		}
		registeredHash, _ := networkspec.Hash(registered)
		if registeredHash != hash {
			return
		}
		_ = tunnel.WriteStatus(connection, nil)
		var buffer [1]byte
		_, _ = connection.Read(buffer[:])
	}()
	bridge := &testBridge{address: testAddress("127.0.0.1:43123")}
	ticketCalls := 0
	runtime, err := Start(context.Background(), profile.Profile{
		ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/relay/tunnel",
	}, remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: "active", Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}, func(context.Context) (remote.RelayTicket, error) {
		ticketCalls++
		return remote.RelayTicket{
			Ticket: "relay-ticket", RelayID: "relay-" + strings.Repeat("a", 64),
			Endpoint: "wss://assigned-relay.example.test/tunnel",
			DeviceID: "22222222-2222-4222-8222-222222222222",
		}, nil
	}, Config{
		ClientVersion: "2.4.0",
		startForwarder: func(ctx context.Context, config websocketmux.ClientConfig) (streamForwarder, error) {
			if config.URL != "wss://assigned-relay.example.test/tunnel" {
				t.Fatalf("WebSocket URL = %q", config.URL)
			}
			if config.ClientVersion != "2.4.0" || config.DeviceID != "22222222-2222-4222-8222-222222222222" {
				t.Fatalf("WSS client identity = version %q device %q", config.ClientVersion, config.DeviceID)
			}
			if _, err := config.TokenSource(ctx); err != nil {
				t.Fatal(err)
			}
			return forwarder, nil
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &readSignalConn{Conn: connection, read: statusRead}, nil
		},
		listenSOCKS: func(_ context.Context, gatewayAddress, listenAddress string, _ tunnel.SessionToken) (localBridge, error) {
			select {
			case <-statusRead:
			default:
				t.Fatal("SOCKS listener started before Data Plane authorization was acknowledged")
			}
			if gatewayAddress != listener.Addr().String() || listenAddress != DefaultListenAddress {
				t.Fatalf("SOCKS addresses = %q %q", gatewayAddress, listenAddress)
			}
			return bridge, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ticketCalls != 1 || runtime.Status().SOCKSAddress != "127.0.0.1:43123" {
		t.Fatalf("ticket calls = %d, status = %#v", ticketCalls, runtime.Status())
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.Done():
	case <-time.After(time.Second):
		t.Fatal("runtime did not close")
	}
	<-serverDone
	_, bridgeClosed := bridge.snapshot()
	if !bridgeClosed {
		t.Fatal("SOCKS bridge was not closed")
	}
}

func TestAssignmentTokenSourceRejectsDeviceDriftWithinPool(t *testing.T) {
	initial := remote.RelayTicket{
		Ticket: "ticket-one", RelayID: "relay-a", Endpoint: "wss://relay.example/tunnel", DeviceID: "device-a",
	}
	source := newAssignmentTokenSource(func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{
			Ticket: "ticket-two", RelayID: initial.RelayID, Endpoint: initial.Endpoint, DeviceID: "device-b",
		}, nil
	}, initial)
	if token, err := source(context.Background()); err != nil || token != initial.Ticket {
		t.Fatalf("initial ticket = %q, %v", token, err)
	}
	if _, err := source(context.Background()); err == nil || !strings.Contains(err.Error(), "assignment changed") {
		t.Fatalf("device drift = %v", err)
	}
}

func TestStartRejectsNetworkSpecHashBeforeTransport(t *testing.T) {
	started := false
	_, err := Start(context.Background(), profile.Profile{ID: "service", BaseURL: "https://gateway.example.test"}, remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: "active", Generation: 1,
		NetworkSpec: networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}}, NetworkSpecHash: "bad",
	}, func(context.Context) (remote.RelayTicket, error) { return remote.RelayTicket{Ticket: "ticket"}, nil }, Config{
		startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) {
			started = true
			return nil, errors.New("unexpected")
		},
	})
	if err == nil || started {
		t.Fatalf("error = %v, transport started = %t", err, started)
	}
}

func TestStartClosesTransportWhenAuthorizationIsRejected(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	client, gateway := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		defer gateway.Close()
		if _, readErr := tunnel.ReadSessionHeader(gateway); readErr != nil {
			serverDone <- readErr
			return
		}
		if _, readErr := tunnel.ReadAuthorizedControlSpec(gateway); readErr != nil {
			serverDone <- readErr
			return
		}
		serverDone <- tunnel.WriteStatus(gateway, errors.New("authorization denied"))
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwarder := &testForwarder{Listener: listener}
	bridgeStarted := false
	_, err = Start(context.Background(), profile.Profile{ID: "service", BaseURL: "https://gateway.example.test"}, remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "ticket"}, nil
	}, Config{
		startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) { return forwarder, nil },
		dialContext:    func(context.Context, string, string) (net.Conn, error) { return client, nil },
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
			bridgeStarted = true
			return nil, errors.New("unexpected")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "authorization denied") {
		t.Fatalf("start error = %v", err)
	}
	if bridgeStarted || forwarder.closeCalls != 1 {
		t.Fatalf("bridge started=%t forwarder closes=%d", bridgeStarted, forwarder.closeCalls)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestStartClosesAuthorizedTransportWhenSOCKSBridgeFails(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	client, gateway := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		defer gateway.Close()
		if _, readErr := tunnel.ReadSessionHeader(gateway); readErr != nil {
			serverDone <- readErr
			return
		}
		if _, readErr := tunnel.ReadAuthorizedControlSpec(gateway); readErr != nil {
			serverDone <- readErr
			return
		}
		if writeErr := tunnel.WriteStatus(gateway, nil); writeErr != nil {
			serverDone <- writeErr
			return
		}
		var buffer [1]byte
		_, readErr := gateway.Read(buffer[:])
		if !errors.Is(readErr, io.EOF) {
			serverDone <- readErr
			return
		}
		serverDone <- nil
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwarder := &testForwarder{Listener: listener}
	bridgeFailure := errors.New("SOCKS bind failed")
	_, err = Start(context.Background(), profile.Profile{ID: "service", BaseURL: "https://gateway.example.test"}, remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "ticket"}, nil
	}, Config{
		startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) { return forwarder, nil },
		dialContext:    func(context.Context, string, string) (net.Conn, error) { return client, nil },
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
			return nil, bridgeFailure
		},
	})
	if !errors.Is(err, bridgeFailure) || forwarder.closeCalls != 1 {
		t.Fatalf("start error=%v forwarder closes=%d", err, forwarder.closeCalls)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestStartUsesAutomaticPortWhenDefaultSOCKSAddressIsBusy(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	client, gateway := net.Pipe()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer gateway.Close()
		_, _ = tunnel.ReadSessionHeader(gateway)
		_, _ = tunnel.ReadAuthorizedControlSpec(gateway)
		_ = tunnel.WriteStatus(gateway, nil)
		var buffer [1]byte
		_, _ = gateway.Read(buffer[:])
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwarder := &testForwarder{Listener: listener}
	bridge := &testBridge{address: testAddress("127.0.0.1:43124")}
	var listenAddresses []string
	runtime, err := Start(context.Background(), profile.Profile{
		ID: "service", BaseURL: "https://gateway.example.test",
	}, remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "ticket"}, nil
	}, Config{
		startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) {
			return forwarder, nil
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		listenSOCKS: func(_ context.Context, _, address string, _ tunnel.SessionToken) (localBridge, error) {
			listenAddresses = append(listenAddresses, address)
			if len(listenAddresses) == 1 {
				return nil, errors.New("listen tcp 127.0.0.1:1080: bind: address already in use")
			}
			return bridge, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{DefaultListenAddress, "127.0.0.1:0"}; !reflect.DeepEqual(listenAddresses, want) {
		t.Fatalf("SOCKS listen addresses = %#v, want %#v", listenAddresses, want)
	}
	if runtime.Status().SOCKSAddress != bridge.Addr().String() {
		t.Fatalf("SOCKS status = %#v", runtime.Status())
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	<-serverDone
}

func TestURLUsesSameOriginAndDiscoveredPath(t *testing.T) {
	tests := []struct {
		profile profile.Profile
		want    string
	}{
		{profile: profile.Profile{BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}, want: "wss://gateway.example.test/tunnel"},
		{profile: profile.Profile{BaseURL: "http://gateway.example.test:8080", TunnelPath: "/relay"}, want: "ws://gateway.example.test:8080/relay"},
	}
	for _, test := range tests {
		got, err := URL(test.profile)
		if err != nil || got != test.want {
			t.Fatalf("URL(%#v) = %q, %v; want %q", test.profile, got, err, test.want)
		}
	}
	if _, err := URL(profile.Profile{BaseURL: "https://gateway.example.test", TunnelPath: "https://evil.test/tunnel"}); err == nil {
		t.Fatal("cross-origin tunnel URL accepted")
	}
	if _, err := URL(profile.Profile{BaseURL: "https://gateway.example.test/base", TunnelPath: "/tunnel"}); err == nil {
		t.Fatal("service address with a path was accepted")
	}
}

func TestTransportURLUsesProfileSchemeForAssignedRelay(t *testing.T) {
	for _, test := range []struct {
		baseURL string
		want    string
	}{
		{baseURL: "https://gateway.example.test", want: "wss://relay.example.test/tunnel"},
		{baseURL: "http://gateway.example.test", want: "ws://relay.example.test/tunnel"},
	} {
		got, err := transportURL(
			profile.Profile{BaseURL: test.baseURL},
			"wss://relay.example.test/tunnel",
		)
		if err != nil || got != test.want {
			t.Fatalf("transportURL(%q) = %q, %v; want %q", test.baseURL, got, err, test.want)
		}
	}
}

func TestRuntimeNetworkSettingsCommitOnlyAfterCoreUpdate(t *testing.T) {
	dnsFailure := errors.New("DNS update failed")
	hostsFailure := errors.New("host alias update failed")
	core := &testCore{
		done: make(chan struct{}), dnsErr: dnsFailure, hostsErr: hostsFailure,
	}
	runtime := &Runtime{
		session: remote.Session{Namespace: "payments"}, tun: core,
		dnsNamespace: "development",
		hostAliases:  []singbox.HostAlias{{Domain: "old.example.test", IP: "10.0.0.8"}},
	}
	if err := runtime.UpdateDNSNamespace(context.Background(), "observability"); !errors.Is(err, dnsFailure) {
		t.Fatalf("DNS update error = %v", err)
	}
	if err := runtime.UpdateHostAliases(context.Background(), []singbox.HostAlias{
		{Domain: "new.example.test", IP: "10.0.0.9"},
	}); !errors.Is(err, hostsFailure) {
		t.Fatalf("host alias update error = %v", err)
	}
	if runtime.dnsNamespace != "development" || len(runtime.hostAliases) != 1 ||
		runtime.hostAliases[0].Domain != "old.example.test" {
		t.Fatalf("failed updates changed cached settings: namespace=%q aliases=%#v", runtime.dnsNamespace, runtime.hostAliases)
	}
}

func TestRuntimeStoresNormalizedNetworkSettingsBeforeTUNStarts(t *testing.T) {
	runtime := &Runtime{session: remote.Session{Namespace: "payments"}}
	if err := runtime.UpdateDNSNamespace(context.Background(), "  observability  "); err != nil {
		t.Fatal(err)
	}
	if err := runtime.UpdateHostAliases(context.Background(), []singbox.HostAlias{
		{Domain: "API.Example.Test.", IP: "10.0.0.9"},
	}); err != nil {
		t.Fatal(err)
	}
	if runtime.dnsNamespace != "observability" || len(runtime.hostAliases) != 1 ||
		runtime.hostAliases[0].Domain != "api.example.test" {
		t.Fatalf("cached settings = namespace=%q aliases=%#v", runtime.dnsNamespace, runtime.hostAliases)
	}
	if err := runtime.UpdateHostAliases(context.Background(), []singbox.HostAlias{
		{Domain: "bad domain", IP: "10.0.0.9"},
	}); err == nil {
		t.Fatal("invalid host alias was cached")
	}
}

func TestRuntimeDiagnosticsRequireTUNAndDelegateErrors(t *testing.T) {
	runtime := &Runtime{}
	if _, err := runtime.Metrics(context.Background()); err == nil {
		t.Fatal("metrics succeeded without TUN")
	}
	if _, err := runtime.Logs(context.Background()); err == nil {
		t.Fatal("logs succeeded without TUN")
	}
	if _, err := runtime.ConfigJSON(); err == nil {
		t.Fatal("config succeeded without TUN")
	}
	metricsFailure := errors.New("metrics failed")
	logsFailure := errors.New("logs failed")
	runtime.tun = &testCore{done: make(chan struct{}), metricsErr: metricsFailure, logsErr: logsFailure}
	if _, err := runtime.Metrics(context.Background()); !errors.Is(err, metricsFailure) {
		t.Fatalf("metrics error = %v", err)
	}
	if _, err := runtime.Logs(context.Background()); !errors.Is(err, logsFailure) {
		t.Fatalf("logs error = %v", err)
	}
	config, err := runtime.ConfigJSON()
	if err != nil || string(config) != "{\"version\":2}" {
		t.Fatalf("config = %q, %v", config, err)
	}
}

func TestRuntimeCloseReportsAllErrorsAndClosesResourcesOnce(t *testing.T) {
	coreFailure := errors.New("close TUN")
	bridgeFailure := errors.New("close bridge")
	controlFailure := errors.New("close control")
	forwarderFailure := errors.New("close forwarder")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	core := &testCore{done: make(chan struct{}), closeErr: coreFailure}
	bridge := &testBridge{address: testAddress("127.0.0.1:45010"), closeErr: bridgeFailure}
	control := &testCloseConn{Conn: client, closeErr: controlFailure}
	forwarder := &testForwarder{Listener: listener, closeErr: forwarderFailure}
	runtime := &Runtime{
		ctx: ctx, cancel: cancel, tun: core, bridge: bridge, control: control, forwarder: forwarder,
		done: make(chan struct{}), transportDone: make(chan struct{}),
	}
	closeErr := runtime.Close()
	for _, expected := range []error{coreFailure, bridgeFailure, controlFailure, forwarderFailure} {
		if !errors.Is(closeErr, expected) {
			t.Fatalf("close error %v does not contain %v", closeErr, expected)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if core.closeCalls != 1 || bridge.closeCalls != 1 || control.closeCalls != 1 || forwarder.closeCalls != 1 {
		t.Fatalf("close calls: core=%d bridge=%d control=%d forwarder=%d", core.closeCalls, bridge.closeCalls, control.closeCalls, forwarder.closeCalls)
	}
	select {
	case <-runtime.Done():
	default:
		t.Fatal("runtime done signal remained open")
	}
}

func TestReconnectKeepsSuccessfulTransportContextAlive(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: "active",
		Generation: 1, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 2)
	var contextsMu sync.Mutex
	contexts := make([]<-chan struct{}, 0, 2)
	runtime, err := Start(context.Background(), serverProfile, session, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "relay-ticket"}, nil
	}, Config{
		startForwarder: func(ctx context.Context, config websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := config.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			contextsMu.Lock()
			contexts = append(contexts, ctx.Done())
			contextsMu.Unlock()
			go acceptTestControlWithSignal(listener, controls)
			return &testForwarder{Listener: listener}, nil
		},
		listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
			return &testBridge{address: testAddress("127.0.0.1:45002")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstTransportDone := runtime.TransportDone()
	if err := receiveControl(t, controls).Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstTransportDone:
	case <-time.After(time.Second):
		t.Fatal("initial transport did not stop")
	}
	session.Generation++
	if err := runtime.Reconnect(context.Background(), serverProfile, session, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "fresh-relay-ticket"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	secondControl := receiveControl(t, controls)
	defer secondControl.Close()
	contextsMu.Lock()
	if len(contexts) != 2 {
		contextsMu.Unlock()
		t.Fatalf("transport contexts = %d, want 2", len(contexts))
	}
	reconnectedContext := contexts[1]
	contextsMu.Unlock()
	select {
	case <-reconnectedContext:
		t.Fatal("successful reconnect transport context was cancelled")
	case <-time.After(25 * time.Millisecond):
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reconnectedContext:
	case <-time.After(time.Second):
		t.Fatal("Runtime close did not cancel reconnect transport context")
	}
}

func TestReconnectCannotPublishGenerationOlderThanRuntime(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: "active", Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 4)
	bridge := &testBridge{address: testAddress("127.0.0.1:45003")}
	runtime, err := Start(context.Background(), serverProfile, session, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "relay-ticket"}, nil
	}, Config{
		startForwarder: func(ctx context.Context, config websocketmux.ClientConfig) (streamForwarder, error) {
			if _, err := config.TokenSource(ctx); err != nil {
				return nil, err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
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
	defer runtime.Close()
	failedDone := runtime.TransportDone()
	if err := receiveControl(t, controls).Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-failedDone:
	case <-time.After(time.Second):
		t.Fatal("initial transport did not stop")
	}
	newest := session
	newest.Generation = 3
	if err := runtime.AdvanceSession(newest); err != nil {
		t.Fatal(err)
	}
	stale := session
	stale.Generation = 2
	if err := runtime.Reconnect(context.Background(), serverProfile, stale, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "stale-relay-ticket"}, nil
	}); err == nil || !strings.Contains(err.Error(), "stale or changed Session generation") {
		t.Fatalf("stale reconnect error = %v", err)
	}
	_ = receiveControl(t, controls).Close()
	if status := runtime.Status(); status.SessionGeneration != 3 {
		t.Fatalf("stale reconnect published status %#v", status)
	}
	if gatewayAddress, _ := bridge.snapshot(); gatewayAddress != "127.0.0.1:0" {
		t.Fatalf("stale reconnect restored bridge target %q", gatewayAddress)
	}
	if err := runtime.Reconnect(context.Background(), serverProfile, newest, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "fresh-relay-ticket"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	defer receiveControl(t, controls).Close()
	if status := runtime.Status(); status.SessionGeneration != 3 || status.State != "connected" {
		t.Fatalf("fresh reconnect status = %#v", status)
	}
	wantToken, err := tunnel.RelaySessionToken(newest.ID, newest.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if gatewayAddress, token := bridge.transport(); gatewayAddress == "127.0.0.1:0" || token != wantToken {
		t.Fatalf("fresh bridge transport = address %q token %x, want token %x", gatewayAddress, token, wantToken)
	}
}

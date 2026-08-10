package dataplane

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/websocketmux"
)

type testForwarder struct{ net.Listener }

func (forwarder *testForwarder) Address() string { return forwarder.Listener.Addr().String() }

type testBridge struct {
	mu      sync.Mutex
	address net.Addr
	closed  bool
	gateway string
	token   tunnel.SessionToken
}

func (bridge *testBridge) Addr() net.Addr { return bridge.address }
func (bridge *testBridge) Close() error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.closed = true
	return nil
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
	controlAccepted := make(chan struct{})
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
		close(controlAccepted)
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
		listenSOCKS: func(_ context.Context, gatewayAddress, listenAddress string, _ tunnel.SessionToken) (localBridge, error) {
			select {
			case <-controlAccepted:
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

func TestURLUsesSameOriginAndDiscoveredPath(t *testing.T) {
	tests := []struct {
		profile profile.Profile
		want    string
	}{
		{profile: profile.Profile{BaseURL: "https://gateway.example.test", TunnelPath: "/tunnel"}, want: "wss://gateway.example.test/tunnel"},
		{profile: profile.Profile{BaseURL: "http://127.0.0.1:8080", TunnelPath: "/relay"}, want: "ws://127.0.0.1:8080/relay"},
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

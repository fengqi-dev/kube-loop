package dataplane

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

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
		defer checkTestClose(t, connection.Close)
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
	forwardCore := &testForwardCore{address: "127.0.0.1:43124", done: make(chan struct{})}
	ticketCalls := 0
	runtime, err := Start(context.Background(), profile.Profile{
		ID: "service", BaseURL: "https://gateway.example.test", TunnelPath: "/relay/tunnel",
	}, remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: dataplaneSessionActive, Generation: 1,
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
		ForwardStart: func(_ context.Context, options ForwardOptions) (ForwardCore, error) {
			if options.Endpoint != "wss://assigned-relay.example.test/tunnel" ||
				options.RelayTicket != "relay-ticket" || options.Generation != 1 {
				t.Fatalf("forward options = %#v", options)
			}
			return forwardCore, nil
		},
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
		listenSOCKS: func(_ context.Context, listenAddress string) (localBridge, error) {
			select {
			case <-statusRead:
			default:
				t.Fatal("SOCKS listener started before Data Plane authorization was acknowledged")
			}
			if listenAddress != DefaultListenAddress {
				t.Fatalf("SOCKS address = %q", listenAddress)
			}
			return bridge, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ticketCalls != 2 || runtime.Status().SOCKSAddress != "127.0.0.1:43123" {
		t.Fatalf("ticket calls = %d, status = %#v", ticketCalls, runtime.Status())
	}
	bridge.mu.Lock()
	forwardSet := bridge.forwardSet
	bridge.mu.Unlock()
	if !forwardSet {
		t.Fatal("SOCKS bridge did not switch to the Trojan forward runtime")
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
	_, err := Start(
		context.Background(),
		profile.Profile{ID: "service", BaseURL: "https://gateway.example.test"},
		remote.Session{
			ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: dataplaneSessionActive, Generation: 1,
			NetworkSpec: networkspec.Spec{PodCIDRs: []string{"10.42.0.0/16"}}, NetworkSpecHash: "bad",
		},
		func(context.Context) (remote.RelayTicket, error) { return remote.RelayTicket{Ticket: "ticket"}, nil },
		Config{
			startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) {
				started = true
				return nil, errors.New("unexpected")
			},
		},
	)
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
		defer checkTestClose(t, gateway.Close)
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
	_, err = Start(
		context.Background(),
		profile.Profile{ID: "service", BaseURL: "https://gateway.example.test"},
		remote.Session{
			ID: uuid.NewString(), Namespace: "development", State: dataplaneSessionActive, Generation: 1,
			NetworkSpec: spec, NetworkSpecHash: hash,
		},
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "ticket"}, nil
		},
		Config{
			startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) { return forwarder, nil },
			dialContext:    func(context.Context, string, string) (net.Conn, error) { return client, nil },
			listenSOCKS: func(context.Context, string) (localBridge, error) {
				bridgeStarted = true
				return nil, errors.New("unexpected")
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "authorization denied") {
		t.Fatalf("start error = %v", err)
	}
	if closeCalls := forwarder.closeCalls.Load(); bridgeStarted || closeCalls != 1 {
		t.Fatalf("bridge started=%t forwarder closes=%d", bridgeStarted, closeCalls)
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
		defer checkTestClose(t, gateway.Close)
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
	bridgeFailure := errors.New("sOCKS bind failed")
	_, err = Start(
		context.Background(),
		profile.Profile{ID: "service", BaseURL: "https://gateway.example.test"},
		remote.Session{
			ID: uuid.NewString(), Namespace: "development", State: dataplaneSessionActive, Generation: 1,
			NetworkSpec: spec, NetworkSpecHash: hash,
		},
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "ticket"}, nil
		},
		Config{
			startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) { return forwarder, nil },
			dialContext:    func(context.Context, string, string) (net.Conn, error) { return client, nil },
			listenSOCKS: func(context.Context, string) (localBridge, error) {
				return nil, bridgeFailure
			},
		},
	)
	if closeCalls := forwarder.closeCalls.Load(); !errors.Is(err, bridgeFailure) || closeCalls != 1 {
		t.Fatalf("start error=%v forwarder closes=%d", err, closeCalls)
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
		defer checkTestClose(t, gateway.Close)
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
		ID: uuid.NewString(), Namespace: "development", State: dataplaneSessionActive, Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}, func(context.Context) (remote.RelayTicket, error) {
		return remote.RelayTicket{Ticket: "ticket"}, nil
	}, Config{
		startForwarder: func(context.Context, websocketmux.ClientConfig) (streamForwarder, error) {
			return forwarder, nil
		},
		dialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil },
		listenSOCKS: func(_ context.Context, address string) (localBridge, error) {
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
		{
			profile: profile.Profile{BaseURL: "https://gateway.example.test", TunnelPath: defaultTunnelPath},
			want:    "wss://gateway.example.test/tunnel",
		},
		{
			profile: profile.Profile{BaseURL: "http://gateway.example.test:8080", TunnelPath: "/relay"},
			want:    "ws://gateway.example.test:8080/relay",
		},
	}
	for _, test := range tests {
		got, err := URL(test.profile)
		if err != nil || got != test.want {
			t.Fatalf("URL(%#v) = %q, %v; want %q", test.profile, got, err, test.want)
		}
	}
	if _, err := URL(
		profile.Profile{BaseURL: "https://gateway.example.test", TunnelPath: "https://evil.test/tunnel"},
	); err == nil {
		t.Fatal("cross-origin tunnel URL accepted")
	}
	if _, err := URL(
		profile.Profile{BaseURL: "https://gateway.example.test/base", TunnelPath: defaultTunnelPath},
	); err == nil {
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

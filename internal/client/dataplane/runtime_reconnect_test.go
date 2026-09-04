package dataplane

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func TestReconnectKeepsSuccessfulTransportContextAlive(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{
		ID:         "service",
		BaseURL:    "https://gateway.example.test",
		TunnelPath: defaultTunnelPath,
	}
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: dataplaneSessionActive,
		Generation: 1, NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 2)
	var contextsMu sync.Mutex
	contexts := make([]<-chan struct{}, 0, 2)
	runtime, err := Start(
		context.Background(),
		serverProfile,
		session,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "relay-ticket"}, nil
		},
		Config{
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
		},
	)
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
	if err := runtime.Reconnect(
		context.Background(),
		serverProfile,
		session,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "fresh-relay-ticket"}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	secondControl := receiveControl(t, controls)
	defer checkTestClose(t, secondControl.Close)
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
	serverProfile := profile.Profile{
		ID:         "service",
		BaseURL:    "https://gateway.example.test",
		TunnelPath: defaultTunnelPath,
	}
	session := remote.Session{
		ID: "ec0b67a2-e84c-4fe7-a0c5-810f210157b5", State: dataplaneSessionActive, Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}
	controls := make(chan net.Conn, 4)
	bridge := &testBridge{address: testAddress("127.0.0.1:45003")}
	runtime, err := Start(
		context.Background(),
		serverProfile,
		session,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "relay-ticket"}, nil
		},
		Config{
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
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, runtime.Close)
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
	runtime.stateMu.Lock()
	runtime.session = newest
	runtime.status.SessionGeneration = newest.Generation
	runtime.stateMu.Unlock()
	stale := session
	stale.Generation = 2
	if err := runtime.Reconnect(
		context.Background(),
		serverProfile,
		stale,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "stale-relay-ticket"}, nil
		},
	); err == nil ||
		!strings.Contains(err.Error(), "stale or changed Session generation") {
		t.Fatalf("stale reconnect error = %v", err)
	}
	_ = receiveControl(t, controls).Close()
	if status := runtime.Status(); status.SessionGeneration != 3 {
		t.Fatalf("stale reconnect published status %#v", status)
	}
	if gatewayAddress, _ := bridge.snapshot(); gatewayAddress != "127.0.0.1:0" {
		t.Fatalf("stale reconnect restored bridge target %q", gatewayAddress)
	}
	if err := runtime.Reconnect(
		context.Background(),
		serverProfile,
		newest,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "fresh-relay-ticket"}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	freshControl := receiveControl(t, controls)
	defer checkTestClose(t, freshControl.Close)
	if status := runtime.Status(); status.SessionGeneration != 3 || status.State != dataplaneConnected {
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

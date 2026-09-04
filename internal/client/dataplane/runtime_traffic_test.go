package dataplane

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
	"github.com/google/uuid"
)

func TestRuntimeOpenTrafficStreamUsesCurrentTunnelTransport(t *testing.T) {
	token, err := tunnel.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	client, gateway := net.Pipe()
	forwarder := &testForwarder{open: func(context.Context) (net.Conn, error) { return client, nil }}
	runtime := &Runtime{
		ctx: context.Background(), forwarder: forwarder, token: token, transportDone: make(chan struct{}),
	}
	serverDone := make(chan error, 1)
	go func() {
		defer checkTestClose(t, gateway.Close)
		header, readErr := tunnel.ReadSessionHeader(gateway)
		if readErr != nil {
			serverDone <- readErr
			return
		}
		request, readErr := tunnel.ReadTrafficOpenBody(gateway)
		if readErr != nil || header.Command != tunnel.CommandTraffic || header.Token != token ||
			request.Mode != tunnel.TrafficModePreview || request.TaskID != taskID {
			serverDone <- errors.Join(readErr, errors.New("traffic open request changed"))
			return
		}
		if writeErr := tunnel.WriteStatus(gateway, nil); writeErr != nil {
			serverDone <- writeErr
			return
		}
		framed, frameErr := trafficstream.Accept(t.Context(), gateway)
		if frameErr != nil {
			serverDone <- frameErr
			return
		}
		ready, frameErr := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Ready})
		if frameErr == nil {
			frameErr = framed.WriteFrame(t.Context(), ready)
		}
		serverDone <- frameErr
	}()
	framed, err := runtime.OpenTrafficStream(t.Context(), tunnel.TrafficModePreview, taskID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := framed.ReadFrame(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil || frame.Type != exchangestream.Ready {
		t.Fatalf("ready frame = %#v, err = %v", frame, err)
	}
	_ = framed.Close()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestReconnectDrainsTransportUntilTrafficStreamCloses(t *testing.T) {
	initialSpec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	initialHash, err := networkspec.Hash(initialSpec)
	if err != nil {
		t.Fatal(err)
	}
	refreshedSpec, err := networkspec.Normalize(networkspec.Spec{
		ServiceIPs: []string{"10.96.0.10", "10.110.108.72"},
	})
	if err != nil {
		t.Fatal(err)
	}
	refreshedHash, err := networkspec.Hash(refreshedSpec)
	if err != nil {
		t.Fatal(err)
	}
	session := remote.Session{
		ID: uuid.NewString(), Namespace: "default", State: dataplaneSessionActive, Generation: 1,
		NetworkSpec: initialSpec, NetworkSpecHash: initialHash,
	}
	serverProfile := profile.Profile{
		ID:         "service",
		BaseURL:    "https://gateway.example.test",
		TunnelPath: defaultTunnelPath,
	}
	controls := make(chan net.Conn, 2)
	forwarders := make(chan *testForwarder, 2)
	trafficClient, trafficGateway := net.Pipe()
	trafficServerDone := make(chan error, 1)
	continueTraffic := make(chan struct{})
	go func() {
		defer checkTestClose(t, trafficGateway.Close)
		if _, readErr := tunnel.ReadTrafficOpen(trafficGateway); readErr != nil {
			trafficServerDone <- readErr
			return
		}
		if writeErr := tunnel.WriteStatus(trafficGateway, nil); writeErr != nil {
			trafficServerDone <- writeErr
			return
		}
		framed, acceptErr := trafficstream.AcceptWithEncryption(t.Context(), trafficGateway, false)
		if acceptErr != nil {
			trafficServerDone <- acceptErr
			return
		}
		defer checkTestClose(t, framed.Close)
		ready, encodeErr := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Ready})
		if encodeErr == nil {
			encodeErr = framed.WriteFrame(t.Context(), ready)
		}
		if encodeErr != nil {
			trafficServerDone <- encodeErr
			return
		}
		<-continueTraffic
		data, encodeErr := exchangestream.Encode(exchangestream.Frame{
			Type: exchangestream.Data, StreamID: 1, Payload: []byte("still-open"),
		})
		if encodeErr == nil {
			encodeErr = framed.WriteFrame(t.Context(), data)
		}
		trafficServerDone <- encodeErr
	}()
	starts := 0
	runtime, err := Start(
		context.Background(),
		serverProfile,
		session,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "relay-ticket"}, nil
		},
		Config{
			TrafficEncryption: new(false),
			startForwarder: func(ctx context.Context, config websocketmux.ClientConfig) (streamForwarder, error) {
				if _, tokenErr := config.TokenSource(ctx); tokenErr != nil {
					return nil, tokenErr
				}
				listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
				if listenErr != nil {
					return nil, listenErr
				}
				forwarder := &testForwarder{Listener: listener}
				if starts == 0 {
					forwarder.open = func(context.Context) (net.Conn, error) { return trafficClient, nil }
				}
				starts++
				forwarders <- forwarder
				go acceptTestControlWithSignal(listener, controls)
				return forwarder, nil
			},
			listenSOCKS: func(context.Context, string, string, tunnel.SessionToken) (localBridge, error) {
				return &testBridge{address: testAddress("127.0.0.1:45020")}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	firstForwarder := <-forwarders
	firstControl := receiveControl(t, controls)
	defer checkTestClose(t, firstControl.Close)
	stream, err := runtime.OpenTrafficStream(t.Context(), tunnel.TrafficModePreview, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	ready, err := stream.ReadFrame(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if frame, decodeErr := exchangestream.Decode(ready); decodeErr != nil || frame.Type != exchangestream.Ready {
		t.Fatalf("Preview ready = %#v, %v", frame, decodeErr)
	}

	refreshed := session
	refreshed.Generation++
	refreshed.NetworkSpec = refreshedSpec
	refreshed.NetworkSpecHash = refreshedHash
	if err := runtime.Reconnect(
		context.Background(),
		serverProfile,
		refreshed,
		func(context.Context) (remote.RelayTicket, error) {
			return remote.RelayTicket{Ticket: "refreshed-relay-ticket"}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	secondForwarder := <-forwarders
	secondControl := receiveControl(t, controls)
	defer checkTestClose(t, secondControl.Close)
	if closeCalls := firstForwarder.closeCalls.Load(); closeCalls != 0 {
		t.Fatalf("old transport closed with active Preview stream: closes=%d", closeCalls)
	}
	close(continueTraffic)
	data, err := stream.ReadFrame(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := exchangestream.Decode(data)
	if err != nil || frame.Type != exchangestream.Data || string(frame.Payload) != "still-open" {
		t.Fatalf("Preview frame after transport swap = %#v, %v", frame, err)
	}
	if err := <-trafficServerDone; err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if closeCalls := firstForwarder.closeCalls.Load(); closeCalls != 1 {
		t.Fatalf("drained transport closes=%d, want 1", closeCalls)
	}
	if closeCalls := secondForwarder.closeCalls.Load(); closeCalls != 0 {
		t.Fatalf("current transport closed while draining old transport: closes=%d", closeCalls)
	}
}

func TestRuntimeOpenTrafficStreamDoesNotHoldTransportLockAcrossStartup(t *testing.T) {
	token, err := tunnel.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	client, gateway := net.Pipe()
	tracked := &testCloseConn{Conn: client}
	forwarder := &testForwarder{open: func(context.Context) (net.Conn, error) { return tracked, nil }}
	transportDone := make(chan struct{})
	runtime := &Runtime{
		ctx: context.Background(), forwarder: forwarder, token: token, transportDone: transportDone,
	}
	opened := make(chan error, 1)
	go func() {
		_, openErr := runtime.OpenTrafficStream(t.Context(), tunnel.TrafficModeMirror, uuid.NewString())
		opened <- openErr
	}()
	if _, err := tunnel.ReadTrafficOpen(gateway); err != nil {
		t.Fatal(err)
	}

	lockAcquired := make(chan struct{})
	go func() {
		runtime.transportMu.Lock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("Traffic startup held transportMu across status I/O")
	}
	runtime.forwarder = nil
	runtime.token = tunnel.SessionToken{}
	closeSignal(runtime.transportDone)
	runtime.transportMu.Unlock()
	if err := tunnel.WriteStatus(gateway, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-opened:
		if err == nil || !strings.Contains(err.Error(), "transport changed") {
			t.Fatalf("OpenTrafficStream error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Traffic startup did not observe transport replacement")
	}
	if tracked.closeCalls != 1 {
		t.Fatalf("failed Traffic stream close calls = %d, want 1", tracked.closeCalls)
	}
	_ = gateway.Close()
}

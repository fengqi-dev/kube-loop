package exchange

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/google/uuid"
)

type testExchangeClient struct {
	connection *trafficstream.FrameConn
	openErr    error
	task       remote.ExchangeTask

	mu        sync.Mutex
	openCalls int
	stopCalls int
}

func (client *testExchangeClient) CreateExchange(
	context.Context,
	profile.Profile,
	remote.Session,
	remote.ExchangeSpec,
	string,
) (remote.ExchangeTask, error) {
	return client.task, nil
}

func (client *testExchangeClient) OpenTrafficStream(
	_ context.Context, profileID, mode, taskID string,
) (*trafficstream.FrameConn, error) {
	client.mu.Lock()
	client.openCalls++
	client.mu.Unlock()
	if profileID != "server" || mode != tunnel.TrafficModeExchange || taskID != client.task.ID {
		return nil, errors.New("Exchange Traffic stream selector changed")
	}
	return client.connection, client.openErr
}

func (client *testExchangeClient) StopExchange(
	_ context.Context,
	_ profile.Profile,
	_ remote.Session,
	_ string,
) (remote.ExchangeTask, error) {
	client.mu.Lock()
	client.stopCalls++
	client.mu.Unlock()
	task := client.task
	task.State = "stopping"
	return task, nil
}

func (client *testExchangeClient) calls() (int, int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.openCalls, client.stopCalls
}

func TestManagerRelaysTCPAndUDPOnlyToRetainedLocalTargets(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcpListener.Close()
	tcpPort := uint16(tcpListener.Addr().(*net.TCPAddr).Port)
	tcpDone := make(chan error, 1)
	go func() {
		connection, acceptErr := tcpListener.Accept()
		if acceptErr != nil {
			tcpDone <- acceptErr
			return
		}
		defer connection.Close()
		request := make([]byte, len("cluster-request"))
		if _, readErr := io.ReadFull(connection, request); readErr != nil || string(request) != "cluster-request" {
			tcpDone <- errors.Join(readErr, errors.New("unexpected TCP request"))
			return
		}
		if _, writeErr := connection.Write([]byte("local-response")); writeErr != nil {
			tcpDone <- writeErr
			return
		}
		if closeErr := connection.(*net.TCPConn).CloseWrite(); closeErr != nil {
			tcpDone <- closeErr
			return
		}
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		buffer := make([]byte, 1)
		_, readErr := connection.Read(buffer)
		if !errors.Is(readErr, io.EOF) {
			tcpDone <- readErr
			return
		}
		tcpDone <- nil
	}()

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer udpListener.Close()
	udpPort := uint16(udpListener.LocalAddr().(*net.UDPAddr).Port)
	udpDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		_ = udpListener.SetReadDeadline(time.Now().Add(3 * time.Second))
		count, remoteAddress, readErr := udpListener.ReadFromUDP(buffer)
		if readErr != nil || string(buffer[:count]) != "udp-request" {
			udpDone <- errors.Join(readErr, errors.New("unexpected UDP request"))
			return
		}
		_, writeErr := udpListener.WriteToUDP([]byte("udp-response"), remoteAddress)
		udpDone <- writeErr
	}()

	connection, gatewayConnection := mustTrafficConnections(t)
	serverDone := make(chan error, 1)
	relayVerified := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Ready}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{
			Type: exchangestream.Open, StreamID: 1, ServicePort: 80, Protocol: exchangestream.ProtocolTCP,
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{
			Type: exchangestream.Data, StreamID: 1, Payload: []byte("cluster-request"),
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		data, readErr := readTestFrame(ctx, gatewayConnection)
		if readErr != nil || data.Type != exchangestream.Data || data.StreamID != 1 || string(data.Payload) != "local-response" {
			serverDone <- errors.Join(readErr, errors.New("unexpected local TCP response"))
			return
		}
		halfClose, readErr := readTestFrame(ctx, gatewayConnection)
		if readErr != nil || halfClose.Type != exchangestream.CloseWrite || halfClose.StreamID != 1 {
			serverDone <- errors.Join(readErr, errors.New("missing local TCP half-close"))
			return
		}
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.CloseWrite, StreamID: 1}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Close, StreamID: 1}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{
			Type: exchangestream.Datagram, StreamID: 2, ServicePort: 53,
			Protocol: exchangestream.ProtocolUDP, Payload: []byte("udp-request"),
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		datagram, readErr := readTestFrame(ctx, gatewayConnection)
		if readErr != nil || datagram.Type != exchangestream.Datagram || datagram.StreamID != 2 ||
			datagram.ServicePort != 53 || string(datagram.Payload) != "udp-response" {
			serverDone <- errors.Join(readErr, fmt.Errorf("unexpected local UDP response: %#v", datagram))
			return
		}
		if writeErr := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Close, StreamID: 2}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		close(relayVerified)
		stop, readErr := readTestFrame(ctx, gatewayConnection)
		if readErr != nil || stop.Type != exchangestream.Stop {
			serverDone <- errors.Join(readErr, errors.New("missing client Exchange stop"))
			return
		}
		_ = writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Stop})
		_ = gatewayConnection.Close()
		serverDone <- nil
	}()

	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	task := remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", ClusterIP: "10.96.0.20",
		Ports:     []remote.ExchangePort{{ServicePort: 53, Protocol: "udp"}, {ServicePort: 80, Protocol: "tcp"}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}
	client := &testExchangeClient{connection: connection, task: task}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "server", BaseURL: "https://gateway.example.test"}
	info, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Service: "api",
		Targets: []LocalTarget{
			{ServicePort: 53, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: udpPort},
			{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: tcpPort},
		},
	})
	if err != nil || info.State != "running" || len(manager.List(serverProfile.ID)) != 1 {
		t.Fatalf("started Exchange=%#v list=%#v err=%v", info, manager.List(serverProfile.ID), err)
	}
	select {
	case err := <-tcpDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local TCP relay timed out")
	}
	select {
	case err := <-udpDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local UDP relay timed out")
	}
	select {
	case <-relayVerified:
	case <-time.After(5 * time.Second):
		t.Fatal("Gateway did not verify local relay responses")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(stopContext, "other-server", task.ID); err == nil {
		t.Fatal("another profile stopped the active Exchange")
	}
	if err := manager.Stop(stopContext, serverProfile.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(stopContext, serverProfile.ID, task.ID); err == nil {
		t.Fatal("repeated Exchange stop succeeded")
	}
	if len(manager.List("")) != 0 {
		t.Fatalf("Exchange remained active: %#v", manager.List(""))
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 1 {
		t.Fatalf("client calls: open=%d stop=%d", openCalls, stopCalls)
	}
}

func TestManagerCompensatesWhenExchangeStreamCannotOpen(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	client := &testExchangeClient{openErr: errors.New("stream unavailable"), task: remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", Ports: []remote.ExchangePort{{ServicePort: 80, Protocol: "tcp"}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server"}, session, Request{
		ProfileID: "server", Service: "api",
		Targets: []LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
	})
	if err == nil {
		t.Fatal("Exchange started without a local stream")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 1 || len(manager.List("")) != 0 {
		t.Fatalf("client calls open=%d stop=%d active=%#v", openCalls, stopCalls, manager.List(""))
	}
}

func TestManagerRejectsUnboundExchangeBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	client := &testExchangeClient{openErr: errors.New("stream unavailable"), task: remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", Ports: []remote.ExchangePort{{ServicePort: 80, Protocol: "tcp"}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ProfileID: "server", Service: "api", Targets: []LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}}}
	if _, err := manager.Start(nil, profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("nil context was accepted")
	}
	request.ProfileID = "other-server"
	if _, err := manager.Start(context.Background(), profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("wrong profile was accepted")
	}
	request.ProfileID = "server"
	session.State = "stopped"
	if _, err := manager.Start(context.Background(), profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("inactive Session was accepted")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 0 || stopCalls != 0 || len(manager.List("")) != 0 {
		t.Fatalf("client calls open=%d stop=%d active=%#v", openCalls, stopCalls, manager.List(""))
	}
}

func TestNormalizeTargetsDefaultsAndRejectsUnsafeExchangeTargets(t *testing.T) {
	targets, ports, err := normalizeTargets([]LocalTarget{{ServicePort: 8080}})
	if err != nil || len(targets) != 1 || targets[0].Protocol != "tcp" ||
		targets[0].LocalHost != "127.0.0.1" || targets[0].LocalPort != 8080 || len(ports) != 1 {
		t.Fatalf("normalized targets=%#v ports=%#v err=%v", targets, ports, err)
	}
	invalid := [][]LocalTarget{
		nil,
		{{ServicePort: 0}},
		{{ServicePort: 80, LocalHost: "0.0.0.0"}},
		{{ServicePort: 80, LocalHost: "bad host"}},
		{{ServicePort: 80}, {ServicePort: 80}},
		{{ServicePort: 80, Protocol: "sctp"}},
	}
	for _, input := range invalid {
		if _, _, err := normalizeTargets(input); err == nil {
			t.Fatalf("unsafe Exchange targets accepted: %#v", input)
		}
	}
}

func TestManagerRejectsGatewayPortSubstitutionBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	client := &testExchangeClient{task: remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", ClusterIP: "10.96.0.20", Ports: []remote.ExchangePort{{ServicePort: 81, Protocol: "tcp"}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server"}, session, Request{
		ProfileID: "server", Service: "api",
		Targets: []LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
	})
	if err == nil {
		t.Fatal("Gateway port substitution was accepted")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 0 || stopCalls != 1 {
		t.Fatalf("client calls: open=%d stop=%d", openCalls, stopCalls)
	}
}

func writeTestFrame(ctx context.Context, connection *trafficstream.FrameConn, frame exchangestream.Frame) error {
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		return err
	}
	return connection.WriteFrame(ctx, encoded)
}

func readTestFrame(ctx context.Context, connection *trafficstream.FrameConn) (exchangestream.Frame, error) {
	encoded, err := connection.ReadFrame(ctx)
	if err != nil {
		return exchangestream.Frame{}, err
	}
	return exchangestream.Decode(encoded)
}

func mustTrafficConnections(t *testing.T) (*trafficstream.FrameConn, *trafficstream.FrameConn) {
	t.Helper()
	clientSide, gatewaySide := net.Pipe()
	accepted := make(chan struct {
		connection *trafficstream.FrameConn
		err        error
	}, 1)
	go func() {
		connection, err := trafficstream.Accept(t.Context(), gatewaySide)
		accepted <- struct {
			connection *trafficstream.FrameConn
			err        error
		}{connection: connection, err: err}
	}()
	client, err := trafficstream.Dial(t.Context(), clientSide)
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if server.err != nil {
		t.Fatal(server.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.connection.Close()
	})
	return client, server.connection
}

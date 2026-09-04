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

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

func TestManagerDeleteReportsNotManagedLocally(t *testing.T) {
	t.Parallel()
	client := &testExchangeClient{}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(
		context.Background(), "server-1", uuid.NewString(),
	); !errors.Is(err, ErrNotManagedLocally) {
		t.Fatalf("Delete() error = %v", err)
	}
}

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
		return nil, errors.New("exchange Traffic stream selector changed")
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
	tcpPort, tcpDone := startExchangeTCPRelay(t)
	udpPort, udpDone := startExchangeUDPRelay(t)

	connection, gatewayConnection := mustTrafficConnections(t)
	serverDone, relayVerified := startExchangeGateway(t, gatewayConnection)

	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     exchangeSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	task := remote.ExchangeTask{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		Namespace: session.Namespace,
		State:     "pending",
		Service:   "api",
		ClusterIP: "10.96.0.20",
		Ports: []remote.ExchangePort{
			{ServicePort: 53, Protocol: exchangeProtocolUDP},
			{ServicePort: 80, Protocol: exchangeProtocolTCP},
		},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: session.ExpiresAt,
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
			{ServicePort: 53, Protocol: exchangeProtocolUDP, LocalHost: exchangeLoopbackHost, LocalPort: udpPort},
			{ServicePort: 80, Protocol: exchangeProtocolTCP, LocalHost: exchangeLoopbackHost, LocalPort: tcpPort},
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

func TestManagerRejectsStartAfterShutdown(t *testing.T) {
	client := &testExchangeClient{}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
	if err := manager.StopProfile(nil, "server"); err == nil {
		t.Fatal("nil StopProfile context was accepted")
	}
	if err := manager.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(
		t.Context(),
		profile.Profile{ID: "server"},
		remote.Session{State: exchangeSessionActive},
		Request{ProfileID: "server"},
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Shutdown error = %v, want ErrClosed", err)
	}
}

func startExchangeTCPRelay(t *testing.T) (uint16, chan error) {
	t.Helper()
	listener, err := net.Listen(exchangeProtocolTCP, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkTestClose(t, listener.Close) })
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer checkTestClose(t, connection.Close)
		request := make([]byte, len("cluster-request"))
		if _, readErr := io.ReadFull(connection, request); readErr != nil || string(request) != "cluster-request" {
			done <- errors.Join(readErr, errors.New("unexpected TCP request"))
			return
		}
		if _, writeErr := connection.Write([]byte("local-response")); writeErr != nil {
			done <- writeErr
			return
		}
		if closeErr := connection.(*net.TCPConn).CloseWrite(); closeErr != nil {
			done <- closeErr
			return
		}
		_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
		buffer := make([]byte, 1)
		_, readErr := connection.Read(buffer)
		if !errors.Is(readErr, io.EOF) {
			done <- readErr
			return
		}
		done <- nil
	}()
	return port, done
}

func startExchangeUDPRelay(t *testing.T) (uint16, chan error) {
	t.Helper()
	listener, err := net.ListenUDP(exchangeProtocolUDP, &net.UDPAddr{IP: net.ParseIP(exchangeLoopbackHost)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkTestClose(t, listener.Close) })
	port := uint16(listener.LocalAddr().(*net.UDPAddr).Port)
	done := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		_ = listener.SetReadDeadline(time.Now().Add(3 * time.Second))
		count, remoteAddress, readErr := listener.ReadFromUDP(buffer)
		if readErr != nil || string(buffer[:count]) != "udp-request" {
			done <- errors.Join(readErr, errors.New("unexpected UDP request"))
			return
		}
		_, writeErr := listener.WriteToUDP([]byte("udp-response"), remoteAddress)
		done <- writeErr
	}()
	return port, done
}

func startExchangeGateway(
	t *testing.T,
	connection *trafficstream.FrameConn,
) (chan error, chan struct{}) {
	t.Helper()
	done := make(chan error, 1)
	verified := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		for _, frame := range []exchangestream.Frame{
			{Type: exchangestream.Ready},
			{Type: exchangestream.Open, StreamID: 1, ServicePort: 80, Protocol: exchangestream.ProtocolTCP},
			{Type: exchangestream.Data, StreamID: 1, Payload: []byte("cluster-request")},
		} {
			if err := writeTestFrame(ctx, connection, frame); err != nil {
				done <- err
				return
			}
		}
		data, err := readTestFrame(ctx, connection)
		if err != nil || data.Type != exchangestream.Data || data.StreamID != 1 ||
			string(data.Payload) != "local-response" {
			done <- errors.Join(err, errors.New("unexpected local TCP response"))
			return
		}
		halfClose, err := readTestFrame(ctx, connection)
		if err != nil || halfClose.Type != exchangestream.CloseWrite || halfClose.StreamID != 1 {
			done <- errors.Join(err, errors.New("missing local TCP half-close"))
			return
		}
		for _, frame := range []exchangestream.Frame{
			{Type: exchangestream.CloseWrite, StreamID: 1},
			{Type: exchangestream.Close, StreamID: 1},
			{
				Type: exchangestream.Datagram, StreamID: 2, ServicePort: 53,
				Protocol: exchangestream.ProtocolUDP, Payload: []byte("udp-request"),
			},
		} {
			if err := writeTestFrame(ctx, connection, frame); err != nil {
				done <- err
				return
			}
		}
		datagram, err := readTestFrame(ctx, connection)
		if err != nil || datagram.Type != exchangestream.Datagram || datagram.StreamID != 2 ||
			datagram.ServicePort != 53 || string(datagram.Payload) != "udp-response" {
			done <- errors.Join(err, fmt.Errorf("unexpected local UDP response: %#v", datagram))
			return
		}
		if err := writeTestFrame(
			ctx,
			connection,
			exchangestream.Frame{Type: exchangestream.Close, StreamID: 2},
		); err != nil {
			done <- err
			return
		}
		close(verified)
		stop, err := readTestFrame(ctx, connection)
		if err != nil || stop.Type != exchangestream.Stop {
			done <- errors.Join(err, errors.New("missing client Exchange stop"))
			return
		}
		_ = writeTestFrame(ctx, connection, exchangestream.Frame{Type: exchangestream.Stop})
		_ = connection.Close()
		done <- nil
	}()
	return done, verified
}

func TestManagerCompensatesWhenExchangeStreamCannotOpen(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     exchangeSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	client := &testExchangeClient{openErr: errors.New("stream unavailable"), task: remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", Ports: []remote.ExchangePort{{ServicePort: 80, Protocol: exchangeProtocolTCP}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server"}, session, Request{
		ProfileID: "server",
		Service:   "api",
		Targets: []LocalTarget{
			{ServicePort: 80, Protocol: exchangeProtocolTCP, LocalHost: exchangeLoopbackHost, LocalPort: 8080},
		},
	})
	if err == nil {
		t.Fatal("Exchange started without a local stream")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 1 || len(manager.List("")) != 0 {
		t.Fatalf("client calls open=%d stop=%d active=%#v", openCalls, stopCalls, manager.List(""))
	}
}

func TestManagerStopProfileWaitsForStartingExchange(t *testing.T) {
	connection, gatewayConnection := mustTrafficConnections(t)
	now := time.Now().UTC()
	session := remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: exchangeSessionActive, ExpiresAt: now.Add(time.Hour),
	}
	task := remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "pending", Service: "api", ClusterIP: "10.96.0.20",
		Ports:     []remote.ExchangePort{{ServicePort: 80, Protocol: exchangeProtocolTCP}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}
	client := &testExchangeClient{connection: connection, task: task}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "server"}
	started := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(t.Context(), serverProfile, session, Request{
			ProfileID: "server", Service: "api",
			Targets: []LocalTarget{
				{ServicePort: 80, Protocol: exchangeProtocolTCP, LocalHost: exchangeLoopbackHost, LocalPort: 8080},
			},
		})
		started <- startErr
	}()
	deadline := time.Now().Add(time.Second)
	for {
		openCalls, _ := client.calls()
		if openCalls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Exchange Start did not reach readiness")
		}
		time.Sleep(time.Millisecond)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.StopProfile(t.Context(), "server") }()
	select {
	case err := <-stopped:
		t.Fatalf("StopProfile bypassed an in-flight Exchange Start: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	gatewayDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		if err := writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Ready}); err != nil {
			gatewayDone <- err
			return
		}
		stop, err := readTestFrame(ctx, gatewayConnection)
		if err != nil || stop.Type != exchangestream.Stop {
			gatewayDone <- errors.Join(err, errors.New("missing Exchange stop"))
			return
		}
		_ = writeTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Stop})
		gatewayDone <- nil
	}()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := <-gatewayDone; err != nil {
		t.Fatal(err)
	}
	if items := manager.List("server"); len(items) != 0 {
		t.Fatalf("Exchange committed after StopProfile: %#v", items)
	}
}

func TestManagerRejectsUnboundExchangeBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     exchangeSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	client := &testExchangeClient{openErr: errors.New("stream unavailable"), task: remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", Ports: []remote.ExchangePort{{ServicePort: 80, Protocol: exchangeProtocolTCP}},
		CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ProfileID: "server",
		Service:   "api",
		Targets: []LocalTarget{
			{ServicePort: 80, Protocol: exchangeProtocolTCP, LocalHost: exchangeLoopbackHost, LocalPort: 8080},
		},
	}
	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
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
	if err != nil || len(targets) != 1 || targets[0].Protocol != exchangeProtocolTCP ||
		targets[0].LocalHost != exchangeLoopbackHost || targets[0].LocalPort != 8080 || len(ports) != 1 {
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
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     exchangeSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	client := &testExchangeClient{task: remote.ExchangeTask{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		Namespace: session.Namespace,
		State:     "pending",
		Service:   "api",
		ClusterIP: "10.96.0.20",
		Ports:     []remote.ExchangePort{{ServicePort: 81, Protocol: exchangeProtocolTCP}},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: session.ExpiresAt,
	}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server"}, session, Request{
		ProfileID: "server",
		Service:   "api",
		Targets: []LocalTarget{
			{ServicePort: 80, Protocol: exchangeProtocolTCP, LocalHost: exchangeLoopbackHost, LocalPort: 8080},
		},
	})
	if err == nil {
		t.Fatal("Gateway port substitution was accepted")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 0 || stopCalls != 1 {
		t.Fatalf("client calls: open=%d stop=%d", openCalls, stopCalls)
	}
}

func TestMatchTaskTargetsRejectsDuplicateGatewayPorts(t *testing.T) {
	targets := []LocalTarget{
		{ServicePort: 80, Protocol: exchangeProtocolTCP},
		{ServicePort: 53, Protocol: exchangeProtocolUDP},
	}
	task := remote.ExchangeTask{Ports: []remote.ExchangePort{
		{ServicePort: 80, Protocol: exchangeProtocolTCP},
		{ServicePort: 80, Protocol: exchangeProtocolTCP},
	}}
	if err := matchTaskTargets(task, targets); err == nil {
		t.Fatal("duplicate Gateway Exchange ports were accepted")
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

package preview

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

type testPreviewClient struct {
	connection *trafficstream.FrameConn
	openErr    error
	created    remote.PreviewTask
	running    remote.PreviewTask

	mu          sync.Mutex
	createSpec  remote.PreviewSpec
	openCalls   int
	getCalls    int
	stopCalls   int
	stopTaskIDs []string
}

func (client *testPreviewClient) CreatePreview(
	_ context.Context, _ profile.Profile, _ remote.Session, spec remote.PreviewSpec, _ string,
) (remote.PreviewTask, error) {
	client.mu.Lock()
	client.createSpec = spec
	client.mu.Unlock()
	return client.created, nil
}

func (client *testPreviewClient) GetPreview(
	context.Context, profile.Profile, remote.Session, string,
) (remote.PreviewTask, error) {
	client.mu.Lock()
	client.getCalls++
	client.mu.Unlock()
	return client.running, nil
}

func (client *testPreviewClient) OpenTrafficStream(
	_ context.Context, profileID, mode, taskID string,
) (*trafficstream.FrameConn, error) {
	client.mu.Lock()
	client.openCalls++
	client.mu.Unlock()
	if profileID != "server" || mode != tunnel.TrafficModePreview || taskID != client.created.ID {
		return nil, errors.New("Preview Traffic stream selector changed")
	}
	return client.connection, client.openErr
}

func (client *testPreviewClient) StopPreview(
	_ context.Context, _ profile.Profile, _ remote.Session, taskID string,
) (remote.PreviewTask, error) {
	client.mu.Lock()
	client.stopCalls++
	client.stopTaskIDs = append(client.stopTaskIDs, taskID)
	client.mu.Unlock()
	task := client.running
	task.State = "stopping"
	return task, nil
}

func (client *testPreviewClient) calls() (remote.PreviewSpec, int, int, int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.createSpec, client.openCalls, client.getCalls, client.stopCalls
}

func TestManagerRelaysPreviewTCPAndUDPToRetainedLocalTargets(t *testing.T) {
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
			tcpDone <- errors.Join(readErr, errors.New("unexpected Preview TCP request"))
			return
		}
		_, writeErr := connection.Write([]byte("local-response"))
		tcpDone <- writeErr
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
		_ = udpListener.SetReadDeadline(time.Now().Add(5 * time.Second))
		count, remoteAddress, readErr := udpListener.ReadFromUDP(buffer)
		if readErr != nil || string(buffer[:count]) != "udp-request" {
			udpDone <- errors.Join(readErr, errors.New("unexpected Preview UDP request"))
			return
		}
		_, writeErr := udpListener.WriteToUDP([]byte("udp-response"), remoteAddress)
		udpDone <- writeErr
	}()

	connection, gatewayConnection := mustTrafficConnections(t)
	serverDone := make(chan error, 1)
	relayVerified := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		for _, frame := range []exchangestream.Frame{
			{Type: exchangestream.Ready},
			{Type: exchangestream.Open, StreamID: 1, ServicePort: 80, Protocol: exchangestream.ProtocolTCP},
			{Type: exchangestream.Data, StreamID: 1, Payload: []byte("cluster-request")},
		} {
			if writeErr := writePreviewTestFrame(ctx, gatewayConnection, frame); writeErr != nil {
				serverDone <- writeErr
				return
			}
		}
		data, readErr := readPreviewTestFrame(ctx, gatewayConnection)
		if readErr != nil || data.Type != exchangestream.Data || data.StreamID != 1 || string(data.Payload) != "local-response" {
			serverDone <- errors.Join(readErr, errors.New("unexpected local Preview TCP response"))
			return
		}
		halfClose, readErr := readPreviewTestFrame(ctx, gatewayConnection)
		if readErr != nil || halfClose.Type != exchangestream.CloseWrite || halfClose.StreamID != 1 {
			serverDone <- errors.Join(readErr, errors.New("missing local Preview TCP half-close"))
			return
		}
		if writeErr := writePreviewTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Close, StreamID: 1}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		if writeErr := writePreviewTestFrame(ctx, gatewayConnection, exchangestream.Frame{
			Type: exchangestream.Datagram, StreamID: 2, ServicePort: 53,
			Protocol: exchangestream.ProtocolUDP, Payload: []byte("udp-request"),
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		datagram, readErr := readPreviewTestFrame(ctx, gatewayConnection)
		if readErr != nil || datagram.Type != exchangestream.Datagram || datagram.StreamID != 2 ||
			datagram.ServicePort != 53 || string(datagram.Payload) != "udp-response" {
			serverDone <- errors.Join(readErr, fmt.Errorf("unexpected local Preview UDP response: %#v", datagram))
			return
		}
		if writeErr := writePreviewTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Close, StreamID: 2}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		close(relayVerified)
		stop, readErr := readPreviewTestFrame(ctx, gatewayConnection)
		if readErr != nil || stop.Type != exchangestream.Stop {
			serverDone <- errors.Join(readErr, errors.New("missing client Preview stop"))
			return
		}
		_ = writePreviewTestFrame(ctx, gatewayConnection, exchangestream.Frame{Type: exchangestream.Stop})
		serverDone <- nil
	}()

	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	created := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending", Name: "local-api",
		Ports:     []remote.PreviewPort{{ServicePort: 53, Protocol: "udp"}, {ServicePort: 80, Protocol: "tcp"}},
		CreatedAt: now, UpdatedAt: now,
	}
	running := created
	running.State = "running"
	running.ClusterIP = "10.96.0.42"
	client := &testPreviewClient{connection: connection, created: created, running: running}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "server", BaseURL: "https://gateway.example.test"}
	info, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Namespace: session.Namespace, Name: "local-api",
		Targets: []LocalTarget{
			{ServicePort: 53, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: udpPort},
			{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: tcpPort},
		},
	})
	if err != nil || info.State != "running" || info.ClusterIP != running.ClusterIP || len(manager.List(serverProfile.ID)) != 1 {
		t.Fatalf("started Preview=%#v list=%#v err=%v", info, manager.List(serverProfile.ID), err)
	}
	for _, done := range []chan error{tcpDone, udpDone} {
		select {
		case doneErr := <-done:
			if doneErr != nil {
				t.Fatal(doneErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("local Preview relay timed out")
		}
	}
	select {
	case <-relayVerified:
	case <-time.After(5 * time.Second):
		t.Fatal("Gateway did not verify Preview relay responses")
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(stopContext, "other-server", created.ID); err == nil {
		t.Fatal("another profile stopped the active Preview")
	}
	if err := manager.Stop(stopContext, serverProfile.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(stopContext, serverProfile.ID, created.ID); err == nil {
		t.Fatal("repeated Preview stop succeeded")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	spec, openCalls, getCalls, stopCalls := client.calls()
	if spec.Name != "local-api" || len(spec.Ports) != 2 || openCalls != 1 || getCalls != 1 || stopCalls != 1 || len(manager.List("")) != 0 {
		t.Fatalf("spec=%#v calls open=%d get=%d stop=%d active=%#v", spec, openCalls, getCalls, stopCalls, manager.List(""))
	}
}

func TestManagerCompensatesWhenPreviewStreamCannotOpen(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	created := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending", Name: "local-api",
		Ports: []remote.PreviewPort{{ServicePort: 80, Protocol: "tcp"}}, CreatedAt: now, UpdatedAt: now,
	}
	client := &testPreviewClient{openErr: errors.New("stream unavailable"), created: created, running: created}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server"}, session, Request{
		ProfileID: "server", Namespace: session.Namespace, Name: "local-api",
		Targets: []LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
	})
	if err == nil {
		t.Fatal("Preview started without a local stream")
	}
	_, openCalls, getCalls, stopCalls := client.calls()
	if openCalls != 1 || getCalls != 0 || stopCalls != 1 || len(manager.List("")) != 0 {
		t.Fatalf("calls open=%d get=%d stop=%d active=%#v", openCalls, getCalls, stopCalls, manager.List(""))
	}
}

func TestManagerRejectsUnboundPreviewBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	created := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending", Name: "local-api",
		Ports: []remote.PreviewPort{{ServicePort: 80, Protocol: "tcp"}}, CreatedAt: now, UpdatedAt: now,
	}
	client := &testPreviewClient{openErr: errors.New("stream unavailable"), created: created, running: created}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ProfileID: "server", Namespace: session.Namespace, Name: "local-api", Targets: []LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}}}
	if _, err := manager.Start(nil, profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("nil context was accepted")
	}
	request.ProfileID = "other-server"
	if _, err := manager.Start(context.Background(), profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("wrong profile was accepted")
	}
	request.ProfileID = "server"
	request.Namespace = ""
	if _, err := manager.Start(context.Background(), profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("missing Preview namespace was accepted")
	}
	request.Namespace = "other"
	if _, err := manager.Start(context.Background(), profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("Preview namespace outside the active Session was accepted")
	}
	request.Namespace = session.Namespace
	session.State = "stopped"
	if _, err := manager.Start(context.Background(), profile.Profile{ID: "server"}, session, request); err == nil {
		t.Fatal("inactive Session was accepted")
	}
	_, openCalls, getCalls, stopCalls := client.calls()
	if openCalls != 0 || getCalls != 0 || stopCalls != 0 || len(manager.List("")) != 0 {
		t.Fatalf("calls open=%d get=%d stop=%d active=%#v", openCalls, getCalls, stopCalls, manager.List(""))
	}
}

func TestNormalizeTargetsDefaultsAndRejectsUnsafePreviewTargets(t *testing.T) {
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
			t.Fatalf("unsafe Preview targets accepted: %#v", input)
		}
	}
}

func TestManagerRejectsGatewayPreviewPortSubstitutionBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Hour)}
	created := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending", Name: "local-api",
		Ports:     []remote.PreviewPort{{ServicePort: 81, Protocol: "tcp"}},
		CreatedAt: now, UpdatedAt: now,
	}
	client := &testPreviewClient{created: created, running: created}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server"}, session, Request{
		ProfileID: "server", Namespace: session.Namespace, Name: "local-api",
		Targets: []LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
	})
	if err == nil {
		t.Fatal("Gateway Preview port substitution was accepted")
	}
	_, openCalls, getCalls, stopCalls := client.calls()
	if openCalls != 0 || getCalls != 0 || stopCalls != 1 {
		t.Fatalf("client calls open=%d get=%d stop=%d", openCalls, getCalls, stopCalls)
	}
}

func writePreviewTestFrame(ctx context.Context, connection *trafficstream.FrameConn, frame exchangestream.Frame) error {
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		return err
	}
	return connection.WriteFrame(ctx, encoded)
}

func readPreviewTestFrame(ctx context.Context, connection *trafficstream.FrameConn) (exchangestream.Frame, error) {
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

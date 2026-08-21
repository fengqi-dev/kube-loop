package mirror

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

type testMirrorClient struct {
	connection *trafficstream.FrameConn
	openErr    error
	task       remote.MirrorTask

	mu        sync.Mutex
	openCalls int
	stopCalls int
}

func (client *testMirrorClient) CreateMirror(
	context.Context, profile.Profile, remote.Session, remote.MirrorSpec, string,
) (remote.MirrorTask, error) {
	return client.task, nil
}

func (client *testMirrorClient) OpenTrafficStream(
	_ context.Context, profileID, mode, taskID string,
) (*trafficstream.FrameConn, error) {
	client.mu.Lock()
	client.openCalls++
	client.mu.Unlock()
	if profileID != "server" || mode != tunnel.TrafficModeMirror || taskID != client.task.ID {
		return nil, errors.New("mirror Traffic stream selector changed")
	}
	return client.connection, client.openErr
}

func (client *testMirrorClient) StopMirror(
	context.Context, profile.Profile, remote.Session, string,
) (remote.MirrorTask, error) {
	client.mu.Lock()
	client.stopCalls++
	client.mu.Unlock()
	task := client.task
	task.State = "stopping"
	return task, nil
}

func (client *testMirrorClient) calls() (int, int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.openCalls, client.stopCalls
}

func TestManagerCopiesTCPAndUDPRequestsAndDiscardsShadowResponses(t *testing.T) {
	tcpListener, err := net.Listen(mirrorProtocolTCP, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, tcpListener.Close)
	tcpPort := uint16(tcpListener.Addr().(*net.TCPAddr).Port)
	tcpDone := make(chan error, 1)
	go func() {
		connection, acceptErr := tcpListener.Accept()
		if acceptErr != nil {
			tcpDone <- acceptErr
			return
		}
		defer checkTestClose(t, connection.Close)
		request := make([]byte, len("tcp-request"))
		if _, readErr := io.ReadFull(connection, request); readErr != nil || string(request) != "tcp-request" {
			tcpDone <- errors.Join(readErr, errors.New("unexpected TCP shadow request"))
			return
		}
		_, writeErr := connection.Write([]byte("discard-this-response"))
		tcpDone <- writeErr
	}()

	udpListener, err := net.ListenUDP(mirrorProtocolUDP, &net.UDPAddr{IP: net.ParseIP(mirrorLoopbackHost)})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, udpListener.Close)
	udpPort := uint16(udpListener.LocalAddr().(*net.UDPAddr).Port)
	udpDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		_ = udpListener.SetReadDeadline(time.Now().Add(5 * time.Second))
		count, address, readErr := udpListener.ReadFromUDP(buffer)
		if readErr != nil || string(buffer[:count]) != "udp-request" {
			udpDone <- errors.Join(readErr, errors.New("unexpected UDP shadow request"))
			return
		}
		_, writeErr := udpListener.WriteToUDP([]byte("discard-this-datagram"), address)
		udpDone <- writeErr
	}()

	connection, gatewayConnection := mustTrafficConnections(t)
	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		for _, frame := range []mirrorstream.Frame{
			{Type: mirrorstream.Ready},
			{Type: mirrorstream.Open, StreamID: 1, ServicePort: 80, Protocol: mirrorstream.ProtocolTCP},
			{Type: mirrorstream.Data, StreamID: 1, Payload: []byte("tcp-request")},
			{Type: mirrorstream.CloseWrite, StreamID: 1},
			{Type: mirrorstream.Close, StreamID: 1},
			{Type: mirrorstream.Datagram, StreamID: 2, ServicePort: 53, Protocol: mirrorstream.ProtocolUDP, Payload: []byte("udp-request")},
			{Type: mirrorstream.Close, StreamID: 2},
		} {
			if writeErr := writeMirrorTestFrame(ctx, gatewayConnection, frame); writeErr != nil {
				serverDone <- writeErr
				return
			}
		}
		stop, readErr := readMirrorTestFrame(ctx, gatewayConnection)
		if readErr != nil || stop.Type != mirrorstream.Stop {
			serverDone <- errors.Join(readErr, errors.New("client sent a shadow response or omitted Stop"))
			return
		}
		_ = writeMirrorTestFrame(ctx, gatewayConnection, mirrorstream.Frame{Type: mirrorstream.Stop})
		serverDone <- nil
	}()

	now := time.Now().UTC()
	session := remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: mirrorSessionActive, ExpiresAt: now.Add(time.Hour),
	}
	task := remote.MirrorTask{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		Namespace: session.Namespace,
		State:     "pending",
		Service:   "api",
		ClusterIP: "10.96.0.20",
		Ports: []remote.MirrorPort{
			{ServicePort: 53, Protocol: mirrorProtocolUDP},
			{ServicePort: 80, Protocol: mirrorProtocolTCP},
		},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: session.ExpiresAt,
	}
	client := &testMirrorClient{connection: connection, task: task}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "server", BaseURL: "https://gateway.example.test"}
	info, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Service: "api",
		Targets: []LocalTarget{
			{ServicePort: 53, Protocol: mirrorProtocolUDP, LocalHost: mirrorLoopbackHost, LocalPort: udpPort},
			{ServicePort: 80, Protocol: mirrorProtocolTCP, LocalHost: mirrorLoopbackHost, LocalPort: tcpPort},
		},
	})
	if err != nil || info.State != "running" {
		t.Fatalf("started Mirror=%#v err=%v", info, err)
	}
	for _, done := range []chan error{tcpDone, udpDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("local Mirror shadow timed out")
		}
	}
	stopContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(stopContext, "other-server", task.ID); err == nil {
		t.Fatal("another profile stopped the active Mirror")
	}
	if err := manager.Stop(stopContext, serverProfile.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(stopContext, serverProfile.ID, task.ID); err == nil {
		t.Fatal("repeated Mirror stop succeeded")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 1 || len(manager.List("")) != 0 {
		t.Fatalf("client calls open=%d stop=%d active=%#v", openCalls, stopCalls, manager.List(""))
	}
}

func TestManagerCompensatesWhenMirrorStreamCannotOpen(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     mirrorSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	client := &testMirrorClient{task: remote.MirrorTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", Ports: []remote.MirrorPort{{ServicePort: 80, Protocol: mirrorProtocolTCP}},
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
			{ServicePort: 80, Protocol: mirrorProtocolTCP, LocalHost: mirrorLoopbackHost, LocalPort: 8080},
		},
	})
	if err == nil {
		t.Fatal("Mirror started without a local stream")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 1 || len(manager.List("")) != 0 {
		t.Fatalf("client calls open=%d stop=%d active=%#v", openCalls, stopCalls, manager.List(""))
	}
}

func TestManagerRejectsUnboundMirrorBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     mirrorSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	client := &testMirrorClient{openErr: errors.New("stream unavailable"), task: remote.MirrorTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: "pending",
		Service: "api", Ports: []remote.MirrorPort{{ServicePort: 80, Protocol: mirrorProtocolTCP}},
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
			{ServicePort: 80, Protocol: mirrorProtocolTCP, LocalHost: mirrorLoopbackHost, LocalPort: 8080},
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

func TestNormalizeTargetsDefaultsAndRejectsUnsafeMirrorTargets(t *testing.T) {
	targets, ports, err := normalizeTargets([]LocalTarget{{ServicePort: 8080}})
	if err != nil || len(targets) != 1 || targets[0].Protocol != mirrorProtocolTCP ||
		targets[0].LocalHost != mirrorLoopbackHost || targets[0].LocalPort != 8080 || len(ports) != 1 {
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
			t.Fatalf("unsafe Mirror targets accepted: %#v", input)
		}
	}
}

func TestShadowActorDropsSlowTargetWithoutBlockingProducer(t *testing.T) {
	client, slowTarget := net.Pipe()
	defer checkTestClose(t, slowTarget.Close)
	config := Config{
		ShadowQueueSize: 1, ShadowDialTimeout: time.Second,
		ShadowWriteTimeout: 50 * time.Millisecond, ShadowIdleTimeout: time.Second,
	}
	actor := newShadowActor(
		context.Background(),
		LocalTarget{ServicePort: 80, Protocol: mirrorProtocolTCP, LocalHost: mirrorLoopbackHost, LocalPort: 8080},
		func(context.Context, string, string) (net.Conn, error) { return client, nil },
		config,
	)
	start := time.Now()
	for range 100 {
		actor.enqueue(shadowMessage{payload: []byte("payload")})
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("slow shadow blocked producer for %v", elapsed)
	}
	actor.Close()
}

func TestManagerRejectsGatewayPortSubstitutionBeforeOpeningStream(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     mirrorSessionActive,
		ExpiresAt: now.Add(time.Hour),
	}
	client := &testMirrorClient{task: remote.MirrorTask{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		Namespace: session.Namespace,
		State:     "pending",
		Service:   "api",
		ClusterIP: "10.96.0.20",
		Ports:     []remote.MirrorPort{{ServicePort: 81, Protocol: mirrorProtocolTCP}},
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
			{ServicePort: 80, Protocol: mirrorProtocolTCP, LocalHost: mirrorLoopbackHost, LocalPort: 8080},
		},
	})
	if err == nil {
		t.Fatal("Gateway Mirror port substitution was accepted")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 0 || stopCalls != 1 {
		t.Fatalf("client calls open=%d stop=%d", openCalls, stopCalls)
	}
}

func writeMirrorTestFrame(ctx context.Context, connection *trafficstream.FrameConn, frame mirrorstream.Frame) error {
	encoded, err := mirrorstream.Encode(frame)
	if err != nil {
		return err
	}
	return connection.WriteFrame(ctx, encoded)
}

func readMirrorTestFrame(ctx context.Context, connection *trafficstream.FrameConn) (mirrorstream.Frame, error) {
	encoded, err := connection.ReadFrame(ctx)
	if err != nil {
		return mirrorstream.Frame{}, err
	}
	return mirrorstream.Decode(encoded)
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

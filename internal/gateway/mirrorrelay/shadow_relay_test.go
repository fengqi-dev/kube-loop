package mirrorrelay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
)

func TestMirrorRelayForwardsPrimaryTCPAndUDPAndEmitsShadowFrames(t *testing.T) {
	listeners, err := trafficlistener.Bind("127.0.0.1", []trafficmodel.Port{
		{Name: "http", ServicePort: 8080, Protocol: "tcp"},
		{Name: "dns", ServicePort: 5353, Protocol: "udp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listeners.Close() })
	shadow, gateway := newTrafficPair(t)
	tcpPrimary, tcpBackend := net.Pipe()
	udpPrimary, udpBackend := net.Pipe()
	t.Cleanup(func() {
		_ = tcpBackend.Close()
		_ = udpBackend.Close()
	})
	dialed := make(chan dialRequest, 2)
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		dialed <- dialRequest{network: network, address: address}
		switch network {
		case "tcp":
			return tcpPrimary, nil
		case "udp":
			return udpPrimary, nil
		default:
			return nil, errors.New("unexpected primary network")
		}
	}
	relay, err := New(
		gateway,
		listeners,
		[]trafficcontrol.BackendSet{
			{
				ServicePort: 8080, Protocol: "tcp",
				Targets: []trafficcontrol.BackendTarget{{Address: "10.0.0.8", Port: 18080}},
			},
			{
				ServicePort: 5353, Protocol: "udp",
				Targets: []trafficcontrol.BackendTarget{{Address: "10.0.0.53", Port: 15353}},
			},
		},
		Config{
			PrimaryDialContext: dial,
			PrimaryDialTimeout: time.Second,
			ShadowWriteTimeout: time.Second,
			ShadowQueueSize:    16,
			UDPIdleTimeout:     time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	readyDone := make(chan error, 1)
	go func() { readyDone <- relay.Ready(t.Context()) }()
	if frame := readMirrorFrame(t, shadow); frame.Type != mirrorstream.Ready {
		t.Fatalf("ready frame = %#v", frame)
	}
	if err := <-readyDone; err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- relay.Run(t.Context()) }()
	fixture := relayFixture{listeners: listeners, shadow: shadow, dialed: dialed}
	fixture.verifyPrimaryTCP(t, tcpBackend)
	fixture.verifyPrimaryUDP(t, udpBackend)

	writeMirrorFrame(t, shadow, mirrorstream.Frame{Type: mirrorstream.Stop})
	select {
	case err := <-runDone:
		if !errors.Is(err, ErrClientStopped) {
			t.Fatalf("Run() error = %v, want ErrClientStopped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Mirror Relay")
	}
}

func TestMirrorRelayDropsOverflowedShadowStreamUntilClose(t *testing.T) {
	relay := newMirrorRelay(
		nil,
		&trafficlistener.Listeners{},
		nil,
		Config{ShadowQueueSize: 1},
	)
	if !relay.emit(mirrorstream.Frame{
		Type: mirrorstream.Open, StreamID: 7, ServicePort: 8080, Protocol: mirrorstream.ProtocolTCP,
	}) {
		t.Fatal("initial shadow frame was dropped")
	}
	if relay.emit(mirrorstream.Frame{Type: mirrorstream.Data, StreamID: 7, Payload: []byte("overflow")}) {
		t.Fatal("overflowed shadow frame was accepted")
	}
	<-relay.shadow
	if relay.emit(mirrorstream.Frame{Type: mirrorstream.Data, StreamID: 7, Payload: []byte("after overflow")}) {
		t.Fatal("dropped shadow stream resumed before close")
	}
	if !relay.emit(mirrorstream.Frame{Type: mirrorstream.Close, StreamID: 7}) {
		t.Fatal("terminal shadow frame did not get a release attempt")
	}
	if frame := <-relay.shadow; frame.Type != mirrorstream.Close || frame.StreamID != 7 {
		t.Fatalf("terminal shadow frame = %#v", frame)
	}
}

type relayFixture struct {
	listeners *trafficlistener.Listeners
	shadow    *trafficstream.FrameConn
	dialed    <-chan dialRequest
}

func (fixture relayFixture) verifyPrimaryTCP(t *testing.T, backend net.Conn) {
	t.Helper()
	client, err := net.Dial("tcp", fixture.listeners.TCP[0].Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	assertDial(t, fixture.dialed, "tcp", "10.0.0.8:18080")
	openFrame := readMirrorFrame(t, fixture.shadow)
	if openFrame.Type != mirrorstream.Open || openFrame.StreamID == 0 || openFrame.ServicePort != 8080 ||
		openFrame.Protocol != mirrorstream.ProtocolTCP {
		t.Fatalf("open frame = %#v", openFrame)
	}
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	request := make([]byte, len("request"))
	if _, err := io.ReadFull(backend, request); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request, []byte("request")) {
		t.Fatalf("primary TCP request = %q", request)
	}
	requestFrame := readMirrorFrame(t, fixture.shadow)
	if requestFrame.Type != mirrorstream.Data || requestFrame.StreamID != openFrame.StreamID ||
		!bytes.Equal(requestFrame.Payload, []byte("request")) {
		t.Fatalf("shadow TCP request = %#v", requestFrame)
	}
	if _, err := backend.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("response"))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, []byte("response")) {
		t.Fatalf("primary TCP response = %q", response)
	}
}

func (fixture relayFixture) verifyPrimaryUDP(t *testing.T, backend net.Conn) {
	t.Helper()
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	listenerAddress, ok := fixture.listeners.UDP[0].Connection.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("UDP listener address = %T", fixture.listeners.UDP[0].Connection.LocalAddr())
	}
	if _, err := client.WriteToUDP([]byte("query"), listenerAddress); err != nil {
		t.Fatal(err)
	}
	assertDial(t, fixture.dialed, "udp", "10.0.0.53:15353")
	query := make([]byte, len("query"))
	if _, err := io.ReadFull(backend, query); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(query, []byte("query")) {
		t.Fatalf("primary UDP query = %q", query)
	}
	datagramFrame := readMirrorFrame(t, fixture.shadow)
	if datagramFrame.Type != mirrorstream.Datagram || datagramFrame.StreamID == 0 ||
		datagramFrame.ServicePort != 5353 || datagramFrame.Protocol != mirrorstream.ProtocolUDP ||
		!bytes.Equal(datagramFrame.Payload, []byte("query")) {
		t.Fatalf("shadow UDP query = %#v", datagramFrame)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := backend.Write([]byte("answer"))
		writeDone <- err
	}()
	if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("answer"))
	count, _, err := client.ReadFromUDP(response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response[:count], []byte("answer")) {
		t.Fatalf("primary UDP response = %q", response[:count])
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

type dialRequest struct {
	network string
	address string
}

func assertDial(t *testing.T, requests <-chan dialRequest, expectedNetwork, expectedAddress string) {
	t.Helper()
	select {
	case request := <-requests:
		if request.network != expectedNetwork || request.address != expectedAddress {
			t.Fatalf("dial = %q %q, want %q %q", request.network, request.address, expectedNetwork, expectedAddress)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s primary dial", expectedNetwork)
	}
}

func newTrafficPair(t *testing.T) (*trafficstream.FrameConn, *trafficstream.FrameConn) {
	t.Helper()
	clientConnection, gatewayConnection := net.Pipe()
	accepted := make(chan struct {
		connection *trafficstream.FrameConn
		err        error
	}, 1)
	go func() {
		connection, err := trafficstream.Accept(t.Context(), gatewayConnection)
		accepted <- struct {
			connection *trafficstream.FrameConn
			err        error
		}{connection: connection, err: err}
	}()
	client, err := trafficstream.Dial(t.Context(), clientConnection)
	if err != nil {
		_ = clientConnection.Close()
		_ = gatewayConnection.Close()
		t.Fatal(err)
	}
	gatewayResult := <-accepted
	if gatewayResult.err != nil {
		_ = client.Close()
		_ = gatewayConnection.Close()
		t.Fatal(gatewayResult.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = gatewayResult.connection.Close()
	})
	return client, gatewayResult.connection
}

func writeMirrorFrame(t *testing.T, connection *trafficstream.FrameConn, frame mirrorstream.Frame) {
	t.Helper()
	encoded, err := mirrorstream.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteFrame(t.Context(), encoded); err != nil {
		t.Fatal(err)
	}
}

func readMirrorFrame(t *testing.T, connection *trafficstream.FrameConn) mirrorstream.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	encoded, err := connection.ReadFrame(ctx)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := mirrorstream.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

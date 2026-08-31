package reverse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

func TestRunForwardsTCPAndUDP(t *testing.T) {
	listeners, err := BindListeners("127.0.0.1", []entity.Port{
		{Name: "http", ServicePort: 8080, Protocol: "tcp"},
		{Name: "dns", ServicePort: 5353, Protocol: "udp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listeners.Close() })
	client, gateway := newTrafficPair(t)
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(t.Context(), gateway, listeners, time.Second, time.Now)
	}()

	tcpConnection, err := net.Dial("tcp", listeners.TCP[0].Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tcpConnection.Close() })
	openFrame := readExchangeFrame(t, client)
	if openFrame.Type != exchangestream.Open || openFrame.StreamID == 0 || openFrame.ServicePort != 8080 ||
		openFrame.Protocol != exchangestream.ProtocolTCP {
		t.Fatalf("TCP open frame = %#v", openFrame)
	}
	if _, err := tcpConnection.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	requestFrame := readExchangeFrame(t, client)
	if requestFrame.Type != exchangestream.Data || requestFrame.StreamID != openFrame.StreamID ||
		!bytes.Equal(requestFrame.Payload, []byte("request")) {
		t.Fatalf("TCP request frame = %#v", requestFrame)
	}
	writeExchangeFrame(t, client, exchangestream.Frame{
		Type: exchangestream.Data, StreamID: openFrame.StreamID, Payload: []byte("response"),
	})
	if err := tcpConnection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("response"))
	if _, err := io.ReadFull(tcpConnection, response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, []byte("response")) {
		t.Fatalf("TCP response = %q", response)
	}

	udpConnection, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udpConnection.Close() })
	listenerAddress, ok := listeners.UDP[0].Connection.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("UDP listener address = %T", listeners.UDP[0].Connection.LocalAddr())
	}
	if _, err := udpConnection.WriteToUDP([]byte("query"), listenerAddress); err != nil {
		t.Fatal(err)
	}
	datagramFrame := readExchangeFrame(t, client)
	if datagramFrame.Type != exchangestream.Datagram || datagramFrame.StreamID == 0 ||
		datagramFrame.ServicePort != 5353 || datagramFrame.Protocol != exchangestream.ProtocolUDP ||
		!bytes.Equal(datagramFrame.Payload, []byte("query")) {
		t.Fatalf("UDP query frame = %#v", datagramFrame)
	}
	writeExchangeFrame(t, client, exchangestream.Frame{
		Type: exchangestream.Datagram, StreamID: datagramFrame.StreamID, ServicePort: 5353,
		Protocol: exchangestream.ProtocolUDP, Payload: []byte("answer"),
	})
	if err := udpConnection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	udpResponse := make([]byte, len("answer"))
	count, _, err := udpConnection.ReadFromUDP(udpResponse)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(udpResponse[:count], []byte("answer")) {
		t.Fatalf("UDP response = %q", udpResponse[:count])
	}

	writeExchangeFrame(t, client, exchangestream.Frame{Type: exchangestream.Stop})
	select {
	case err := <-runDone:
		if !errors.Is(err, ErrClientStopped) {
			t.Fatalf("Run() error = %v, want ErrClientStopped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run")
	}
}

func TestWriteFrameSendsEncodedFrame(t *testing.T) {
	client, gateway := newTrafficPair(t)
	written := make(chan error, 1)
	go func() {
		written <- WriteFrame(t.Context(), gateway, exchangestream.Frame{Type: exchangestream.Ready})
	}()
	frame := readExchangeFrame(t, client)
	if frame.Type != exchangestream.Ready {
		t.Fatalf("frame = %#v", frame)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
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

func writeExchangeFrame(t *testing.T, connection *trafficstream.FrameConn, frame exchangestream.Frame) {
	t.Helper()
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteFrame(t.Context(), encoded); err != nil {
		t.Fatal(err)
	}
}

func readExchangeFrame(t *testing.T, connection *trafficstream.FrameConn) exchangestream.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	encoded, err := connection.ReadFrame(ctx)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

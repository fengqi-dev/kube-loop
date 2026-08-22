package reverserelay

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
)

func TestRelayReadReady(t *testing.T) {
	tests := []struct {
		name    string
		frame   exchangestream.Frame
		wantErr bool
	}{
		{name: "ready", frame: exchangestream.Frame{Type: exchangestream.Ready}},
		{name: "unexpected stop", frame: exchangestream.Frame{Type: exchangestream.Stop}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, gateway := newTrafficPair(t)
			relay := New(client, nil, nil)
			encoded, err := exchangestream.Encode(test.frame)
			if err != nil {
				t.Fatal(err)
			}
			writeDone := make(chan error, 1)
			go func() {
				writeDone <- gateway.WriteFrame(t.Context(), encoded)
			}()
			err = relay.ReadReady(t.Context())
			if (err != nil) != test.wantErr {
				t.Fatalf("ReadReady() error = %v, want error = %t", err, test.wantErr)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRelayRunForwardsTCP(t *testing.T) {
	client, gateway := newTrafficPair(t)
	localRelay, localPeer := net.Pipe()
	t.Cleanup(func() { _ = localPeer.Close() })
	dialed := make(chan dialRequest, 1)
	relay := New(
		client,
		[]Target{{ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 18080}},
		func(_ context.Context, network, address string) (net.Conn, error) {
			dialed <- dialRequest{network: network, address: address}
			return localRelay, nil
		},
	)
	runDone := runRelay(t, relay)

	writeExchangeFrame(t, gateway, exchangestream.Frame{
		Type: exchangestream.Open, StreamID: 7, ServicePort: 8080, Protocol: exchangestream.ProtocolTCP,
	})
	assertDial(t, dialed, "tcp", "127.0.0.1:18080")
	writeExchangeFrame(t, gateway, exchangestream.Frame{
		Type: exchangestream.Data, StreamID: 7, Payload: []byte("request"),
	})
	request := make([]byte, len("request"))
	if _, err := localPeer.Read(request); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request, []byte("request")) {
		t.Fatalf("local request = %q", request)
	}

	localWrite := make(chan error, 1)
	go func() {
		_, err := localPeer.Write([]byte("response"))
		localWrite <- err
	}()
	response := readExchangeFrame(t, gateway)
	if response.Type != exchangestream.Data || response.StreamID != 7 ||
		!bytes.Equal(response.Payload, []byte("response")) {
		t.Fatalf("gateway response = %#v", response)
	}
	if err := <-localWrite; err != nil {
		t.Fatal(err)
	}

	writeExchangeFrame(t, gateway, exchangestream.Frame{Type: exchangestream.Stop})
	if err := waitForRun(t, runDone); err != nil {
		t.Fatal(err)
	}
}

func TestRelayRunForwardsUDP(t *testing.T) {
	client, gateway := newTrafficPair(t)
	localRelay, localPeer := net.Pipe()
	t.Cleanup(func() { _ = localPeer.Close() })
	dialed := make(chan dialRequest, 1)
	relay := New(
		client,
		[]Target{{ServicePort: 5353, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: 15353}},
		func(_ context.Context, network, address string) (net.Conn, error) {
			dialed <- dialRequest{network: network, address: address}
			return localRelay, nil
		},
	)
	runDone := runRelay(t, relay)

	writeExchangeFrame(t, gateway, exchangestream.Frame{
		Type: exchangestream.Datagram, StreamID: 9, ServicePort: 5353,
		Protocol: exchangestream.ProtocolUDP, Payload: []byte("query"),
	})
	assertDial(t, dialed, "udp", "127.0.0.1:15353")
	query := make([]byte, len("query"))
	if _, err := localPeer.Read(query); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(query, []byte("query")) {
		t.Fatalf("local datagram = %q", query)
	}

	localWrite := make(chan error, 1)
	go func() {
		_, err := localPeer.Write([]byte("answer"))
		localWrite <- err
	}()
	response := readExchangeFrame(t, gateway)
	if response.Type != exchangestream.Datagram || response.StreamID != 9 || response.ServicePort != 5353 ||
		response.Protocol != exchangestream.ProtocolUDP || !bytes.Equal(response.Payload, []byte("answer")) {
		t.Fatalf("gateway datagram = %#v", response)
	}
	if err := <-localWrite; err != nil {
		t.Fatal(err)
	}

	writeExchangeFrame(t, gateway, exchangestream.Frame{Type: exchangestream.Stop})
	if err := waitForRun(t, runDone); err != nil {
		t.Fatal(err)
	}
}

func TestReverseServicePort(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   uint32
		want    int32
		wantErr bool
	}{
		{name: "minimum", value: 1, want: 1},
		{name: "maximum", value: 65535, want: 65535},
		{name: "zero", wantErr: true},
		{name: "too large", value: 65536, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := reverseServicePort(test.value)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("reverseServicePort(%d) = %d, %v", test.value, got, err)
			}
		})
	}
}

func TestEncodedReverseServicePort(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   int32
		want    uint32
		wantErr bool
	}{
		{name: "minimum", value: 1, want: 1},
		{name: "maximum", value: 65535, want: 65535},
		{name: "zero", wantErr: true},
		{name: "negative", value: -1, wantErr: true},
		{name: "too large", value: 65536, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := encodedReverseServicePort(test.value)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("encodedReverseServicePort(%d) = %d, %v", test.value, got, err)
			}
		})
	}
}

func newTrafficPair(t *testing.T) (*trafficstream.FrameConn, *trafficstream.FrameConn) {
	t.Helper()
	clientConnection, serverConnection := net.Pipe()
	acceptResult := make(chan struct {
		connection *trafficstream.FrameConn
		err        error
	}, 1)
	go func() {
		connection, err := trafficstream.Accept(t.Context(), serverConnection)
		acceptResult <- struct {
			connection *trafficstream.FrameConn
			err        error
		}{connection: connection, err: err}
	}()
	client, err := trafficstream.Dial(t.Context(), clientConnection)
	if err != nil {
		_ = clientConnection.Close()
		_ = serverConnection.Close()
		t.Fatal(err)
	}
	accepted := <-acceptResult
	if accepted.err != nil {
		_ = client.Close()
		_ = serverConnection.Close()
		t.Fatal(accepted.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = accepted.connection.Close()
	})
	return client, accepted.connection
}

func runRelay(t *testing.T, relay *Relay) <-chan error {
	t.Helper()
	runDone := make(chan error, 1)
	go func() {
		runDone <- relay.Run(t.Context())
	}()
	return runDone
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

type dialRequest struct {
	network string
	address string
}

func assertDial(t *testing.T, dialed <-chan dialRequest, expectedNetwork, expectedAddress string) {
	t.Helper()
	select {
	case request := <-dialed:
		if request.network != expectedNetwork || request.address != expectedAddress {
			t.Fatalf("dial = %q %q, want %q %q", request.network, request.address, expectedNetwork, expectedAddress)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s dial", expectedNetwork)
	}
}

func waitForRun(t *testing.T, runDone <-chan error) error {
	t.Helper()
	select {
	case err := <-runDone:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Relay.Run")
		return nil
	}
}

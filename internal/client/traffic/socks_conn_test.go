package traffic

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

type closeWriteConn struct {
	net.Conn

	called bool
	err    error
}

func (connection *closeWriteConn) CloseWrite() error {
	connection.called = true
	return connection.err
}

func TestBufferedConnReadAndCloseWrite(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
	})
	closeErr := errors.New("close write")
	underlying := &closeWriteConn{Conn: client, err: closeErr}
	connection := &bufferedConn{
		Conn: underlying, reader: bufio.NewReader(bytes.NewBufferString("buffered")),
	}
	buffer := make([]byte, len("buffered"))
	if count, err := connection.Read(buffer); err != nil || count != len(buffer) || string(buffer) != "buffered" {
		t.Fatalf("Read() count=%d value=%q err=%v", count, buffer, err)
	}
	if err := connection.CloseWrite(); !errors.Is(err, closeErr) || !underlying.called {
		t.Fatalf("CloseWrite() err=%v called=%v", err, underlying.called)
	}
}

func TestBufferedConnCloseWriteFallsBackToClose(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	connection := &bufferedConn{Conn: client, reader: bufio.NewReader(bytes.NewReader(nil))}
	if err := connection.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("closed")); err == nil {
		t.Fatal("fallback CloseWrite did not close the connection")
	}
}

func TestUDPConnNetworkContracts(t *testing.T) {
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	socket, err := net.DialUDP("udp", nil, relay.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	control, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	connection := &udpConn{
		control: control, socket: socket,
		target: "10.96.0.10:53", targetHost: "10.96.0.10", targetPort: 53,
	}

	if connection.LocalAddr() == nil {
		t.Fatal("LocalAddr() returned nil")
	}
	if address := connection.RemoteAddr(); address.Network() != "udp" || address.String() != "10.96.0.10:53" {
		t.Fatalf("RemoteAddr() = %s %s", address.Network(), address.String())
	}
	deadline := time.Now().Add(time.Minute)
	if err := connection.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("second Close(): %v", err)
	}
}

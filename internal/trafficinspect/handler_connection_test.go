package trafficinspect

import (
	"errors"
	"net"
	"testing"
)

type closeErrorConn struct {
	net.Conn

	closeErr   error
	closeCalls int
}

func (connection *closeErrorConn) Close() error {
	connection.closeCalls++
	return connection.closeErr
}

func TestTransparentConnRetainsCloseError(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = client.Close() })
	closeFailure := errors.New("close transparent connection")
	underlying := &closeErrorConn{Conn: server, closeErr: closeFailure}
	connection := newTransparentConn(underlying)

	for range 2 {
		if err := connection.Close(); !errors.Is(err, closeFailure) {
			t.Fatalf("Close() error = %v, want %v", err, closeFailure)
		}
	}
	if underlying.closeCalls != 1 {
		t.Fatalf("underlying close calls = %d, want 1", underlying.closeCalls)
	}
	select {
	case <-connection.done:
	default:
		t.Fatal("transparent connection done signal remained open")
	}
}

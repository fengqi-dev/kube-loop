package dataplane

import (
	"io"
	"net"
	"net/netip"
	"testing"
)

func TestSOCKSConnectCompletesGatewayPath(t *testing.T) {
	client, server := net.Pipe()
	defer checkTestClose(t, client.Close)
	defer checkTestClose(t, server.Close)
	done := make(chan error, 1)
	go func() {
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(server, greeting); err != nil {
			done <- err
			return
		}
		if _, err := server.Write([]byte{5, 0}); err != nil {
			done <- err
			return
		}
		request := make([]byte, 10)
		if _, err := io.ReadFull(server, request); err != nil {
			done <- err
			return
		}
		_, err := server.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 53})
		done <- err
	}()
	if err := socksConnect(client, netip.MustParseAddr("10.96.0.10"), 53); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKSConnectReportsRejectedTarget(t *testing.T) {
	client, server := net.Pipe()
	defer checkTestClose(t, client.Close)
	defer checkTestClose(t, server.Close)
	go func() {
		_, _ = io.CopyN(io.Discard, server, 3)
		_, _ = server.Write([]byte{5, 0})
		_, _ = io.CopyN(io.Discard, server, 10)
		_, _ = server.Write([]byte{5, 5, 0, 1})
	}()
	if err := socksConnect(client, netip.MustParseAddr("10.96.0.10"), 53); err == nil {
		t.Fatal("rejected SOCKS target unexpectedly passed")
	}
}

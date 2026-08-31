package gateway

import (
	"bufio"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

type delayedRelayReadConn struct {
	net.Conn

	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (connection *delayedRelayReadConn) Read(payload []byte) (int, error) {
	connection.startOnce.Do(func() { close(connection.started) })
	count, err := connection.Conn.Read(payload)
	<-connection.release
	return count, err
}

func TestRelayTCPForwardsBothDirectionsUntilClose(t *testing.T) {
	leftRelay, leftPeer := net.Pipe()
	rightRelay, rightPeer := net.Pipe()
	t.Cleanup(func() {
		_ = leftPeer.Close()
		_ = rightPeer.Close()
	})
	done := make(chan struct{})
	go func() {
		relayTCP(leftRelay, rightRelay)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for _, connection := range []net.Conn{leftPeer, rightPeer} {
		if err := connection.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}
	leftPayload := []byte("left-to-right")
	go func() { _, _ = leftPeer.Write(leftPayload) }()
	actual := make([]byte, len(leftPayload))
	if _, err := io.ReadFull(rightPeer, actual); err != nil || string(actual) != string(leftPayload) {
		t.Fatalf("right payload = %q err = %v", actual, err)
	}
	rightPayload := []byte("right-to-left")
	go func() { _, _ = rightPeer.Write(rightPayload) }()
	actual = make([]byte, len(rightPayload))
	if _, err := io.ReadFull(leftPeer, actual); err != nil || string(actual) != string(rightPayload) {
		t.Fatalf("left payload = %q err = %v", actual, err)
	}
	_ = leftPeer.Close()
	_ = rightPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("TCP relay did not stop after both peers closed")
	}
}

func TestRelayUDPAdaptsFramedClientDatagrams(t *testing.T) {
	clientRelay, clientPeer := net.Pipe()
	targetRelay, targetPeer := net.Pipe()
	t.Cleanup(func() {
		_ = clientPeer.Close()
		_ = targetPeer.Close()
	})
	done := make(chan struct{})
	go func() {
		(&Server{}).relayUDP(clientRelay, targetRelay)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for _, connection := range []net.Conn{clientPeer, targetPeer} {
		if err := connection.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}
	outbound := []byte("client-datagram")
	go func() { _ = tunnel.WriteDatagram(clientPeer, outbound) }()
	actual := make([]byte, len(outbound))
	if _, err := io.ReadFull(targetPeer, actual); err != nil || string(actual) != string(outbound) {
		t.Fatalf("target datagram = %q err = %v", actual, err)
	}
	inbound := []byte("target-datagram")
	go func() { _, _ = targetPeer.Write(inbound) }()
	actual, err := tunnel.ReadDatagram(bufio.NewReader(clientPeer), nil)
	if err != nil || string(actual) != string(inbound) {
		t.Fatalf("client datagram = %q err = %v", actual, err)
	}
	_ = clientPeer.Close()
	_ = targetPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UDP relay did not stop after peers closed")
	}
}

func TestRelayUDPWaitsForClientReader(t *testing.T) {
	clientRelay, clientPeer := net.Pipe()
	targetRelay, targetPeer := net.Pipe()
	defer func() { _ = clientPeer.Close() }()
	defer func() { _ = targetPeer.Close() }()
	release := make(chan struct{})
	client := &delayedRelayReadConn{Conn: clientRelay, started: make(chan struct{}), release: release}
	done := make(chan struct{})
	go func() {
		(&Server{}).relayUDP(client, targetRelay)
		close(done)
	}()
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("UDP client reader did not start")
	}
	_ = targetPeer.Close()
	select {
	case <-done:
		t.Fatal("UDP relay returned before client reader completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("UDP relay did not wait for client reader")
	}
}

func TestValidNetworkSpecHashRequiresLowercaseSHA256(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !validNetworkSpecHash(valid) || validNetworkSpecHash(valid[:63]) ||
		validNetworkSpecHash("A"+valid[1:]) || validNetworkSpecHash("g"+valid[1:]) {
		t.Fatal("NetworkSpec hash validation accepted an invalid form")
	}
}

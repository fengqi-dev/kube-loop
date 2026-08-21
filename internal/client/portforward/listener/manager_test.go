package listener

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type echoTrafficDialer struct {
	targets chan string
}

func (dialer *echoTrafficDialer) DialContext(
	_ context.Context, network, address string,
) (net.Conn, error) {
	dialer.targets <- network + ":" + address
	client, server := net.Pipe()
	go func() {
		_, _ = io.Copy(server, server)
		_ = server.Close()
	}()
	return client, nil
}

func TestStartResolvedTCPPortForward(t *testing.T) {
	dialer := &echoTrafficDialer{targets: make(chan string, 1)}
	manager := NewManager()
	info, err := manager.StartResolved(t.Context(), Request{
		Context: "server", Namespace: "default", Kind: KindPod,
		Name: "api-0", RemotePort: 8080,
	}, "10.244.1.9:8080", dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.StopAll()

	connection, err := net.DialTimeout("tcp", info.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, connection.Close)
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(connection, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != "ping" {
		t.Fatalf("echo = %q", buffer)
	}
	select {
	case target := <-dialer.targets:
		if target != "tcp:10.244.1.9:8080" {
			t.Fatalf("target = %q", target)
		}
	case <-time.After(time.Second):
		t.Fatal("traffic dialer was not used")
	}
}

func TestStartResolvedUDPPortForward(t *testing.T) {
	dialer := &echoTrafficDialer{targets: make(chan string, 1)}
	manager := NewManager()
	info, err := manager.StartResolved(t.Context(), Request{
		Context: "server", Namespace: "default", Kind: KindService,
		Name: "dns", Protocol: "UDP", RemotePort: 53,
	}, "10.96.0.10:53", dialer)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.StopAll()

	remote, err := net.ResolveUDPAddr("udp", info.Address)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, connection.Close)
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write([]byte("dns")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 3)
	if _, err := io.ReadFull(connection, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != "dns" {
		t.Fatalf("echo = %q", buffer)
	}
}

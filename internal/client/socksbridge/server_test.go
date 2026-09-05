package socksbridge

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/things-go/go-socks5/statute"
)

func TestBridgeSetLogHandler(t *testing.T) {
	var messages []string
	bridge := &Bridge{server: &Server{}}
	bridge.SetLogHandler(func(message string) {
		messages = append(messages, message)
	})
	bridge.server.logf("TCP connected %s", "api.example.test:443")
	if len(messages) != 1 || messages[0] != "TCP connected api.example.test:443" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestBridgeHostUDPHandlerSupportsConcurrentUpdates(t *testing.T) {
	server := &Server{}
	bridge := &Bridge{server: server}
	handler := HostUDPHandler(func(string, uint16) (func(context.Context) (net.Conn, error), bool) {
		return func(context.Context) (net.Conn, error) {
			client, peer := net.Pipe()
			_ = peer.Close()
			return client, nil
		}, true
	})
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for index := range 1_000 {
			if index%2 == 0 {
				bridge.SetHostUDPHandler(handler)
			} else {
				bridge.SetHostUDPHandler(nil)
			}
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for range 1_000 {
			connection, _ := server.dial(t.Context(), "udp", "127.0.0.1:53")
			if connection != nil {
				_ = connection.Close()
			}
		}
	}()
	close(start)
	wait.Wait()
}

type forwardDialerFunc func(context.Context, string, string) (net.Conn, error)

func (function forwardDialerFunc) DialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	return function(ctx, network, address)
}

func TestForwardDialerCarriesTCPAndRawUDP(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			client, peer := net.Pipe()
			defer peer.Close()
			server := &Server{}
			bridge := &Bridge{server: server}
			bridge.SetForwardDialer(forwardDialerFunc(func(
				_ context.Context,
				gotNetwork string,
				gotAddress string,
			) (net.Conn, error) {
				if gotNetwork != network || gotAddress != "10.96.0.10:6379" {
					t.Fatalf("DialContext(%q, %q)", gotNetwork, gotAddress)
				}
				return client, nil
			}))
			connection, err := server.dial(context.Background(), network, "10.96.0.10:6379")
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			writeDone := make(chan error, 1)
			go func() { _, writeErr := connection.Write([]byte("ping")); writeDone <- writeErr }()
			payload := make([]byte, 4)
			if _, err := io.ReadFull(peer, payload); err != nil {
				t.Fatal(err)
			}
			if string(payload) != "ping" {
				t.Fatalf("payload = %q", payload)
			}
			if err := <-writeDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBridgeCloseStopsAcceptedConnectionsAndWaitsForHandlers(t *testing.T) {
	bridge, err := Listen(
		context.Background(),
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", bridge.Addr().String())
	if err != nil {
		_ = bridge.Close()
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		bridge.server.tasks.mu.Lock()
		active := bridge.server.tasks.active
		bridge.server.tasks.mu.Unlock()
		if active > 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = bridge.Close()
			t.Fatal("SOCKS handler did not accept connection")
		}
		time.Sleep(time.Millisecond)
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	bridge.server.tasks.mu.Lock()
	active := bridge.server.tasks.active
	bridge.server.tasks.mu.Unlock()
	if active != 0 {
		t.Fatalf("active SOCKS tasks=%d", active)
	}
}

func TestSOCKSUDPPacketRoundTrip(t *testing.T) {
	want := []byte("dns payload")
	packet, err := encodeTestDatagram("10.96.0.10", 53, want)
	if err != nil {
		t.Fatal(err)
	}
	host, port, got, err := decodeTestDatagram(packet)
	if err != nil {
		t.Fatal(err)
	}
	if host != "10.96.0.10" || port != 53 || !bytes.Equal(got, want) {
		t.Fatalf("got %s:%d %q", host, port, got)
	}
}

func TestSOCKSUDPDomainRoundTrip(t *testing.T) {
	packet, err := encodeTestDatagram(
		"kube-dns.kube-system.svc.cluster.local", 53, []byte{1},
	)
	if err != nil {
		t.Fatal(err)
	}
	host, port, _, err := decodeTestDatagram(packet)
	if err != nil {
		t.Fatal(err)
	}
	if host != "kube-dns.kube-system.svc.cluster.local" || port != 53 {
		t.Fatalf("got %s:%d", host, port)
	}
}

func TestRelayPreservesTCPHalfCloseResponse(t *testing.T) {
	clientSide, relayClient := tcpConnectionPair(t)
	defer checkTestClose(t, clientSide.Close)
	defer checkTestClose(t, relayClient.Close)
	relayTarget, targetSide := tcpConnectionPair(t)
	defer checkTestClose(t, relayTarget.Close)
	defer checkTestClose(t, targetSide.Close)

	relayDone := make(chan struct{})
	go func() {
		relay(relayClient, bufio.NewReader(relayClient), relayTarget)
		close(relayDone)
	}()
	backendDone := make(chan error, 1)
	go func() {
		request, err := io.ReadAll(targetSide)
		if err != nil {
			backendDone <- err
			return
		}
		if string(request) != "request" {
			backendDone <- fmt.Errorf("request = %q", request)
			return
		}
		_, err = io.WriteString(targetSide, "response")
		if err == nil {
			err = targetSide.(*net.TCPConn).CloseWrite()
		}
		backendDone <- err
	}()

	_ = clientSide.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(clientSide, "request"); err != nil {
		t.Fatal(err)
	}
	if err := clientSide.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(clientSide)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "response" {
		t.Fatalf("response = %q", response)
	}
	if err := <-backendDone; err != nil {
		t.Fatal(err)
	}
	<-relayDone
}

func tcpConnectionPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, listener.Close)
	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- connection
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case server := <-accepted:
		return client, server
	case err := <-acceptErr:
		checkTestClose(t, client.Close)
		t.Fatal(err)
		return nil, nil
	}
}

func TestHostTCPHandlerBypassesGateway(t *testing.T) {
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, local.Close)
	go func() {
		conn, err := local.Accept()
		if err != nil {
			return
		}
		defer checkTestClose(t, conn.Close)
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		_, _ = conn.Write(append([]byte("local:"), buf[:n]...))
	}()

	server := &Server{
		HostTCP: func(host string, port uint16) (func(net.Conn), bool) {
			if host != "10.105.153.132" || port != 80 {
				return nil, false
			}
			return func(client net.Conn) {
				defer checkTestClose(t, client.Close)
				upstream, err := net.Dial("tcp", local.Addr().String())
				if err != nil {
					return
				}
				defer checkTestClose(t, upstream.Close)
				relay(client, client, upstream)
			}, true
		},
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, listener.Close)
	go func() { _ = server.Serve(listener) }()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, conn.Close)
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	// SOCKS greeting + connect to intercepted ClusterIP.
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	req := []byte{5, 1, 0, 1, 10, 105, 153, 132, 0, 80}
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0 {
		t.Fatalf("socks status=%d", head[1])
	}
	// drain bind addr
	rest := make([]byte, 6)
	if _, err := io.ReadFull(conn, rest); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "local:ping" {
		t.Fatalf("got %q", got)
	}
}

func TestHostUDPHandlerBypassesGateway(t *testing.T) {
	local, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, local.Close)
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := local.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = local.WriteTo(append([]byte("local-udp:"), buf[:n]...), addr)
		}
	}()
	localPort := local.LocalAddr().(*net.UDPAddr).Port

	server := &Server{
		HostUDP: func(host string, port uint16) (func(context.Context) (net.Conn, error), bool) {
			if host != "10.105.153.132" || port != 9090 {
				return nil, false
			}
			return func(ctx context.Context) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(
					ctx, "udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)),
				)
			}, true
		},
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, listener.Close)
	go func() { _ = server.Serve(listener) }()

	control, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, control.Close)
	_ = control.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(control, reply); err != nil {
		t.Fatal(err)
	}
	// UDP ASSOCIATE with a claimed client port that deliberately differs from
	// the socket below. sing-box can behave this way, and a loopback-only bridge
	// must accept the actual datagram source.
	if _, err := control.Write([]byte{5, 3, 0, 1, 127, 0, 0, 1, 0, 9}); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(control, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0 {
		t.Fatalf("socks status=%d", head[1])
	}
	bindIP := make([]byte, 4)
	if _, err := io.ReadFull(control, bindIP); err != nil {
		t.Fatal(err)
	}
	var bindPort [2]byte
	if _, err := io.ReadFull(control, bindPort[:]); err != nil {
		t.Fatal(err)
	}
	relayPort := int(bindPort[0])<<8 | int(bindPort[1])
	relayAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relayPort}

	client, err := net.DialUDP("udp", nil, relayAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, client.Close)
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	packet, err := encodeTestDatagram("10.105.153.132", 9090, []byte("ping"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(packet); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	_, _, payload, err := decodeTestDatagram(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if got := string(payload); got != "local-udp:ping" {
		t.Fatalf("got %q", got)
	}
}

func encodeTestDatagram(host string, port uint16, payload []byte) ([]byte, error) {
	packet, err := statute.NewDatagram(
		net.JoinHostPort(host, strconv.Itoa(int(port))),
		payload,
	)
	if err != nil {
		return nil, err
	}
	return packet.Bytes(), nil
}

func decodeTestDatagram(packet []byte) (string, uint16, []byte, error) {
	value, err := statute.ParseDatagram(packet)
	if err != nil {
		return "", 0, nil, err
	}
	host := value.DstAddr.FQDN
	if host == "" {
		host = value.DstAddr.IP.String()
	}
	return host, uint16(value.DstAddr.Port), value.Data, nil
}

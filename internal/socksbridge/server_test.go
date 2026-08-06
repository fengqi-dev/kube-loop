package socksbridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
	"github.com/things-go/go-socks5/statute"
)

var testSessionToken = tunnel.SessionToken{1}

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

func TestFramedConnAdaptsGatewayDatagrams(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	connection := newFramedConn(local)
	result := make(chan error, 1)
	go func() {
		if err := tunnel.WriteDatagram(remote, []byte("from-gateway")); err != nil {
			result <- err
			return
		}
		payload, err := tunnel.ReadDatagram(bufio.NewReader(remote), nil)
		if err == nil && string(payload) != "to-gateway" {
			err = io.ErrUnexpectedEOF
		}
		result <- err
	}()

	buffer := make([]byte, tunnel.MaxDatagramSize)
	read, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:read]); got != "from-gateway" {
		t.Fatalf("read %q", got)
	}
	if _, err := connection.Write([]byte("to-gateway")); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestDialGatewayTCPPreservesDomain(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	result := make(chan error, 1)
	go func() {
		connection, err := gateway.Accept()
		if err != nil {
			result <- err
			return
		}
		defer connection.Close()
		request, err := tunnel.ReadOpen(connection)
		if err != nil {
			result <- err
			return
		}
		want := tunnel.OpenRequest{
			Command: tunnel.CommandTCP,
			Host:    "echo.default.svc.cluster.local",
			Port:    8080,
		}
		if request != want {
			result <- fmt.Errorf("open request = %#v, want %#v", request, want)
			return
		}
		if err := tunnel.WriteStatus(connection, nil); err != nil {
			result <- err
			return
		}
		var payload [4]byte
		if _, err := io.ReadFull(connection, payload[:]); err != nil {
			result <- err
			return
		}
		_, err = connection.Write(append([]byte("gateway:"), payload[:]...))
		result <- err
	}()

	server := &Server{
		GatewayAddress: gateway.Addr().String(),
		SessionToken:   testSessionToken,
	}
	connection, err := server.dial(
		context.Background(),
		"tcp",
		"echo.default.svc.cluster.local:8080",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("gateway:ping"))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "gateway:ping" {
		t.Fatalf("response = %q", response)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestDialGatewayUDPAdaptsDatagrams(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	result := make(chan error, 1)
	go func() {
		connection, err := gateway.Accept()
		if err != nil {
			result <- err
			return
		}
		defer connection.Close()
		request, err := tunnel.ReadOpen(connection)
		if err != nil {
			result <- err
			return
		}
		want := tunnel.OpenRequest{Command: tunnel.CommandUDP, Host: "10.96.0.10", Port: 53}
		if request != want {
			result <- fmt.Errorf("open request = %#v, want %#v", request, want)
			return
		}
		if err := tunnel.WriteStatus(connection, nil); err != nil {
			result <- err
			return
		}
		payload, err := tunnel.ReadDatagram(bufio.NewReader(connection), nil)
		if err != nil {
			result <- err
			return
		}
		if string(payload) != "query" {
			result <- fmt.Errorf("datagram = %q", payload)
			return
		}
		result <- tunnel.WriteDatagram(connection, []byte("answer"))
	}()

	server := &Server{
		GatewayAddress: gateway.Addr().String(),
		SessionToken:   testSessionToken,
	}
	connection, err := server.dial(context.Background(), "udp", "10.96.0.10:53")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write([]byte("query")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 32)
	read, err := connection.Read(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(response[:read]) != "answer" {
		t.Fatalf("response = %q", response[:read])
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestDialGatewayReturnsStatusError(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	go func() {
		connection, acceptErr := gateway.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		if _, readErr := tunnel.ReadOpen(connection); readErr == nil {
			_ = tunnel.WriteStatus(connection, errors.New("target denied"))
		}
	}()

	server := &Server{
		GatewayAddress: gateway.Addr().String(),
		SessionToken:   testSessionToken,
	}
	_, err = server.dial(context.Background(), "tcp", "10.96.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "target denied") {
		t.Fatalf("dial error = %v", err)
	}
}

func TestHostTCPHandlerBypassesGateway(t *testing.T) {
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		conn, err := local.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		_, _ = conn.Write(append([]byte("local:"), buf[:n]...))
	}()

	server := &Server{
		GatewayAddress: "127.0.0.1:1", // must not be used
		HostTCP: func(host string, port uint16) (func(net.Conn), bool) {
			if host != "10.105.153.132" || port != 80 {
				return nil, false
			}
			return func(client net.Conn) {
				defer client.Close()
				upstream, err := net.Dial("tcp", local.Addr().String())
				if err != nil {
					return
				}
				defer upstream.Close()
				relay(client, client, upstream)
			}, true
		},
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
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
	defer local.Close()
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
		GatewayAddress: "127.0.0.1:1", // must not be used
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
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	control, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
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
	defer client.Close()
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

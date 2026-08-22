package socksbridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/things-go/go-socks5/statute"

	clienttraffic "github.com/fengqi-dev/kube-loop/internal/client/traffic"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
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
	server := &Server{GatewayAddress: "127.0.0.1:0"}
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

var testSessionToken = tunnel.SessionToken{1}

type testTCPInspector struct {
	dial       DialContextFunc
	served     chan string
	closeCalls atomic.Int32
	closeErr   error
}

func (inspector *testTCPInspector) ServeConn(ctx context.Context, client net.Conn, target string) error {
	inspector.served <- target
	upstream, err := inspector.dial(ctx, "tcp", target)
	if err != nil {
		return err
	}
	defer func() { _ = upstream.Close() }()
	relay(client, client, upstream)
	return nil
}

func (inspector *testTCPInspector) Close() error {
	inspector.closeCalls.Add(1)
	return inspector.closeErr
}

func TestBridgeClosePreservesInspectorErrorAfterListenerClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("close inspector")
	inspector := &testTCPInspector{closeErr: want}
	bridge := &Bridge{Listener: listener, server: &Server{inspector: inspector}}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Close(); !errors.Is(err, want) {
		t.Fatalf("close error = %v, want %v", err, want)
	}
	if err := bridge.Close(); !errors.Is(err, want) {
		t.Fatalf("second close error = %v, want %v", err, want)
	}
	if inspector.closeCalls.Load() != 1 {
		t.Fatalf("inspector close calls = %d, want 1", inspector.closeCalls.Load())
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

func TestFramedConnAdaptsGatewayDatagrams(t *testing.T) {
	local, remote := net.Pipe()
	defer checkTestClose(t, local.Close)
	defer checkTestClose(t, remote.Close)
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
	defer checkTestClose(t, gateway.Close)
	result := make(chan error, 1)
	go func() {
		connection, err := gateway.Accept()
		if err != nil {
			result <- err
			return
		}
		defer checkTestClose(t, connection.Close)
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
	defer checkTestClose(t, connection.Close)
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

func TestBridgeTCPInspectorReusesGatewayDialer(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gateway.Close() }()
	gatewayDone := make(chan error, 1)
	go func() {
		connection, acceptErr := gateway.Accept()
		if acceptErr != nil {
			gatewayDone <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		request, readErr := tunnel.ReadOpen(connection)
		if readErr != nil {
			gatewayDone <- readErr
			return
		}
		want := tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: "api.default.svc.cluster.local", Port: 8443}
		if request != want {
			gatewayDone <- fmt.Errorf("open request = %#v, want %#v", request, want)
			return
		}
		if writeErr := tunnel.WriteStatus(connection, nil); writeErr != nil {
			gatewayDone <- writeErr
			return
		}
		var payload [4]byte
		if _, readErr := io.ReadFull(connection, payload[:]); readErr != nil {
			gatewayDone <- readErr
			return
		}
		_, writeErr := connection.Write(append([]byte("inspected:"), payload[:]...))
		gatewayDone <- writeErr
	}()

	inspector := &testTCPInspector{served: make(chan string, 1)}
	bridge, err := Listen(
		t.Context(),
		gateway.Addr().String(),
		"127.0.0.1:0",
		testSessionToken,
		WithTCPInspector(func(dial DialContextFunc) (TCPInspector, error) {
			inspector.dial = dial
			return inspector, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", bridge.Addr().String())
	if err != nil {
		_ = bridge.Close()
		t.Fatal(err)
	}
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(client, methodReply); err != nil {
		t.Fatal(err)
	}
	request := make([]byte, 0, 36)
	request = append(request, 5, 1, 0, 3, 29)
	request = append(request, []byte("api.default.svc.cluster.local")...)
	request = append(request, 0x20, 0xfb)
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0 {
		t.Fatalf("SOCKS reply status = %d", reply[1])
	}
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("inspected:ping"))
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "inspected:ping" {
		t.Fatalf("response = %q", response)
	}
	if target := <-inspector.served; target != "api.default.svc.cluster.local:8443" {
		t.Fatalf("inspected target = %q", target)
	}
	if err := <-gatewayDone; err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if inspector.closeCalls.Load() != 1 {
		t.Fatalf("inspector close calls = %d, want 1", inspector.closeCalls.Load())
	}
}

func TestBridgeInProcessHTTPInspectionThroughGateway(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Origin-Path", request.URL.Path)
		_, _ = io.WriteString(response, "through-relay")
	}))
	defer origin.Close()
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gateway.Close() }()
	gatewayDone := make(chan error, 1)
	go func() {
		connection, acceptErr := gateway.Accept()
		if acceptErr != nil {
			gatewayDone <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		request, readErr := tunnel.ReadOpen(connection)
		if readErr != nil {
			gatewayDone <- readErr
			return
		}
		if request != (tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: "http.test", Port: 80}) {
			gatewayDone <- fmt.Errorf("unexpected inspection open request: %#v", request)
			return
		}
		upstream, dialErr := net.Dial("tcp", origin.Listener.Addr().String())
		if dialErr != nil {
			gatewayDone <- dialErr
			return
		}
		defer func() { _ = upstream.Close() }()
		if writeErr := tunnel.WriteStatus(connection, nil); writeErr != nil {
			gatewayDone <- writeErr
			return
		}
		relay(connection, connection, upstream)
		gatewayDone <- nil
	}()
	authority, err := trafficinspect.LoadOrCreateAuthority(filepath.Join(t.TempDir(), "inspection-ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	inspected := make(chan string, 1)
	bridge, err := Listen(
		t.Context(),
		gateway.Addr().String(),
		"127.0.0.1:0",
		testSessionToken,
		WithTCPInspector(func(dial DialContextFunc) (TCPInspector, error) {
			return trafficinspect.New(trafficinspect.Config{
				CA:          authority.TLSCertificate(),
				DialContext: trafficinspect.DialContextFunc(dial),
				OnRequest: func(request *http.Request) {
					inspected <- request.Method + " " + request.Host + request.URL.Path
				},
				AllowHTTP2: true,
			})
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	transport := &http.Transport{DialContext: (clienttraffic.Dialer{Endpoint: clienttraffic.Endpoint{
		Address: bridge.Addr().String(),
	}}).DialContext}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://http.test/poc", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Close = true
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(errors.Join(readErr, closeErr))
	}
	if response.StatusCode != http.StatusOK || string(body) != "through-relay" ||
		response.Header.Get("X-Origin-Path") != "/poc" {
		t.Fatalf("response status=%d body=%q path=%q", response.StatusCode, body, response.Header.Get("X-Origin-Path"))
	}
	if event := <-inspected; event != "GET http.test/poc" {
		t.Fatalf("inspection event = %q", event)
	}
	if err := <-gatewayDone; err != nil {
		t.Fatal(err)
	}
}

func TestDialGatewayUDPAdaptsDatagrams(t *testing.T) {
	gateway, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, gateway.Close)
	result := make(chan error, 1)
	go func() {
		connection, err := gateway.Accept()
		if err != nil {
			result <- err
			return
		}
		defer checkTestClose(t, connection.Close)
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
	defer checkTestClose(t, connection.Close)
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
	defer checkTestClose(t, gateway.Close)
	go func() {
		connection, acceptErr := gateway.Accept()
		if acceptErr != nil {
			return
		}
		defer checkTestClose(t, connection.Close)
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
		GatewayAddress: "127.0.0.1:1", // must not be used
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

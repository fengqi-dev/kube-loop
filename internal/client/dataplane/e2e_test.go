package dataplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/things-go/go-socks5/statute"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	servermux "github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type echoDialer struct {
	mu              sync.Mutex
	targets         []string
	works           []string
	halfCloseTarget string
}

func (dialer *echoDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	if address != "10.42.0.5:8080" && address != "10.42.0.6:8081" && address != "10.96.0.10:53" {
		return nil, errors.New("unexpected target")
	}
	dialer.mu.Lock()
	dialer.targets = append(dialer.targets, address)
	dialer.works = append(dialer.works, network)
	dialer.mu.Unlock()
	if address == "10.42.0.6:8081" {
		return net.Dial("tcp", dialer.halfCloseTarget)
	}
	client, target := net.Pipe()
	go func() {
		defer func() {
			_ = target.Close() // The mock peer has no test handle and closes only to release the pipe.
		}()
		buffer := make([]byte, 64<<10)
		for {
			read, err := target.Read(buffer)
			if read > 0 {
				if address == "10.96.0.10:53" {
					var query dns.Msg
					if query.Unpack(buffer[:read]) == nil {
						response := new(dns.Msg)
						response.SetReply(&query)
						response.Answer = []dns.RR{&dns.A{
							Hdr: dns.RR_Header{
								Name:   query.Question[0].Name,
								Rrtype: dns.TypeA,
								Class:  dns.ClassINET,
								Ttl:    30,
							},
							A: net.IPv4(10, 42, 0, 5),
						}}
						packed, _ := response.Pack()
						_, _ = target.Write(packed)
					}
				} else {
					_, _ = target.Write(buffer[:read])
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return client, nil
}

func TestAuthenticatedWSSDataPlaneCarriesAuthorizedSOCKSTCPAndUDP(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.42.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		PodIPs:     []string{"10.42.0.5", "10.42.0.6"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := networkspec.Hash(spec)
	sessionID := "ec0b67a2-e84c-4fe7-a0c5-810f210157b5"
	halfCloseTarget, halfCloseResult := startHalfCloseTarget(t)
	dialer := &echoDialer{halfCloseTarget: halfCloseTarget}
	gatewayServer := gateway.NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	gatewayServer.Dialer = dialer
	ticketCalls := 0
	deviceID := "22222222-2222-4222-8222-222222222222"
	handler, err := servermux.NewHandler(servermux.ServerConfig{
		Authenticator: servermux.AuthenticatorFunc(func(request *http.Request) (servermux.Identity, error) {
			if request.Header.Get("Authorization") != "Bearer relay-ticket" {
				return servermux.Identity{}, errors.New("missing RelayTicket")
			}
			return servermux.Identity{
				IdentityID: "identity", DeviceID: deviceID, SessionID: sessionID,
				SessionGeneration: 1, Namespace: "development", NetworkSpecHash: hash,
				ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Handle: func(_ context.Context, identity servermux.Identity, connection net.Conn) {
			gatewayServer.ServeConnForAuthorization(connection, gateway.SessionAuthorization{
				RequestID: identity.RequestID, SessionID: identity.SessionID, Generation: identity.SessionGeneration,
				Namespace:       identity.Namespace,
				NetworkSpecHash: identity.NetworkSpecHash,
			})
		},
		TrafficEncryption: boolPointerForDataplaneTest(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	runtime, err := Start(context.Background(), profile.Profile{
		ID: "service", BaseURL: httpServer.URL, TunnelPath: defaultTunnelPath,
	}, remote.Session{
		ID: sessionID, Namespace: "default", State: dataplaneSessionActive, Generation: 1,
		NetworkSpec: spec, NetworkSpecHash: hash,
	}, func(context.Context) (remote.RelayTicket, error) {
		ticketCalls++
		return remote.RelayTicket{Ticket: "relay-ticket", DeviceID: deviceID}, nil
	}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ticketCalls < 1 {
		t.Fatal("WSS transport did not obtain a RelayTicket")
	}

	socksAddress := runtime.Status().SOCKSAddress
	t.Run("tcp", func(t *testing.T) { testDataPlaneTCP(t, socksAddress) })
	t.Run("udp", func(t *testing.T) { testDataPlaneUDP(t, socksAddress) })
	t.Run("backpressure", func(t *testing.T) { testDataPlaneBackpressure(t, socksAddress) })
	t.Run("half-close", func(t *testing.T) { testDataPlaneHalfClose(t, socksAddress, halfCloseResult) })
	t.Run("dns", func(t *testing.T) { testDataPlaneDNS(t, socksAddress) })

	dialer.mu.Lock()
	if len(dialer.targets) != 5 || dialer.works[0] != "tcp" || dialer.works[1] != "udp" ||
		dialer.works[2] != "tcp" || dialer.works[3] != "tcp" || dialer.works[4] != "udp" ||
		dialer.targets[3] != "10.42.0.6:8081" || dialer.targets[4] != "10.96.0.10:53" {
		t.Fatalf("Data Plane dials = %#v %#v", dialer.works, dialer.targets)
	}
	dialer.mu.Unlock()

	idle := openSOCKSTCP(t, runtime.Status().SOCKSAddress, net.IPv4(10, 42, 0, 5), 8080)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	_ = idle.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := idle.Read(make([]byte, 1)); err == nil {
		t.Fatal("active SOCKS stream survived Data Plane shutdown")
	}
	_ = idle.Close()
}

func boolPointerForDataplaneTest(value bool) *bool { return &value }

func startHalfCloseTarget(t *testing.T) (string, chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkTestClose(t, listener.Close) })
	result := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			result <- acceptErr
			return
		}
		defer checkTestClose(t, connection.Close)
		request, readErr := io.ReadAll(connection)
		if readErr != nil {
			result <- readErr
			return
		}
		if string(request) != "complete request" {
			result <- errors.New("half-close request payload changed")
			return
		}
		_, writeErr := io.WriteString(connection, "response after request EOF")
		result <- writeErr
	}()
	return listener.Addr().String(), result
}

func testDataPlaneTCP(t *testing.T, socksAddress string) {
	t.Helper()
	connection := openSOCKSTCP(t, socksAddress, net.IPv4(10, 42, 0, 5), 8080)
	defer checkTestClose(t, connection.Close)
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	read, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:read]); got != "hello" {
		t.Fatalf("TCP echo = %q", got)
	}
}

func testDataPlaneUDP(t *testing.T, socksAddress string) {
	t.Helper()
	control, relayAddress := openSOCKSUDP(t, socksAddress)
	defer checkTestClose(t, control.Close)
	client, err := net.DialUDP("udp", nil, relayAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, client.Close)
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	packet, err := statute.NewDatagram("10.42.0.5:8080", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(packet.Bytes()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 512)
	read, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := statute.ParseDatagram(buffer[:read])
	if err != nil {
		t.Fatal(err)
	}
	if got := string(reply.Data); got != "hello" {
		t.Fatalf("UDP echo = %q", got)
	}
}

func testDataPlaneBackpressure(t *testing.T, socksAddress string) {
	t.Helper()
	connection := openSOCKSTCP(t, socksAddress, net.IPv4(10, 42, 0, 5), 8080)
	defer checkTestClose(t, connection.Close)
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	payload := bytes.Repeat([]byte("kubeloop-data-plane-"), 64<<10)
	writeResult := make(chan error, 1)
	go func() {
		_, err := connection.Write(payload)
		writeResult <- err
	}()
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, received); err != nil {
		t.Fatal(err)
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, payload) {
		t.Fatal("large TCP payload changed across WSS/smux backpressure")
	}
}

func testDataPlaneHalfClose(t *testing.T, socksAddress string, result <-chan error) {
	t.Helper()
	connection := openSOCKSTCP(t, socksAddress, net.IPv4(10, 42, 0, 6), 8081)
	defer checkTestClose(t, connection.Close)
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(connection, "complete request"); err != nil {
		t.Fatal(err)
	}
	if err := connection.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(connection)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if string(response) != "response after request EOF" {
		t.Fatalf("half-close response = %q", response)
	}
}

func testDataPlaneDNS(t *testing.T, socksAddress string) {
	t.Helper()
	control, relayAddress := openSOCKSUDP(t, socksAddress)
	defer checkTestClose(t, control.Close)
	client, err := net.DialUDP("udp", nil, relayAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, client.Close)
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	query := new(dns.Msg)
	query.SetQuestion("api.default.svc.cluster.local.", dns.TypeA)
	payload, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	packet, err := statute.NewDatagram("10.96.0.10:53", payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(packet.Bytes()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	read, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	replyDatagram, err := statute.ParseDatagram(buffer[:read])
	if err != nil {
		t.Fatal(err)
	}
	var reply dns.Msg
	if err := reply.Unpack(replyDatagram.Data); err != nil {
		t.Fatal(err)
	}
	if len(reply.Answer) != 1 ||
		reply.Answer[0].String() != "api.default.svc.cluster.local.\t30\tIN\tA\t10.42.0.5" {
		t.Fatalf("DNS answer = %#v", reply.Answer)
	}
}

func openSOCKSTCP(t *testing.T, address string, ip net.IP, port uint16) net.Conn {
	t.Helper()
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		checkTestClose(t, connection.Close)
		t.Fatal(err)
	}
	if reply := readFull(t, connection, 2); reply[0] != 5 || reply[1] != 0 {
		checkTestClose(t, connection.Close)
		t.Fatalf("SOCKS greeting = %#v", reply)
	}
	request := []byte{5, 1, 0, 1, ip[12], ip[13], ip[14], ip[15], byte(port >> 8), byte(port)}
	if _, err := connection.Write(request); err != nil {
		checkTestClose(t, connection.Close)
		t.Fatal(err)
	}
	readSOCKSReply(t, connection)
	_ = connection.SetDeadline(time.Time{})
	return connection
}

func openSOCKSUDP(t *testing.T, address string) (net.Conn, *net.UDPAddr) {
	t.Helper()
	control, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	_ = control.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := control.Write([]byte{5, 1, 0}); err != nil {
		checkTestClose(t, control.Close)
		t.Fatal(err)
	}
	if reply := readFull(t, control, 2); reply[1] != 0 {
		checkTestClose(t, control.Close)
		t.Fatalf("SOCKS greeting = %#v", reply)
	}
	if _, err := control.Write([]byte{5, 3, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		checkTestClose(t, control.Close)
		t.Fatal(err)
	}
	reply := readSOCKSReply(t, control)
	port := int(reply[len(reply)-2])<<8 | int(reply[len(reply)-1])
	_ = control.SetDeadline(time.Time{})
	return control, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
}

func readSOCKSReply(t *testing.T, reader io.Reader) []byte {
	t.Helper()
	header := readFull(t, reader, 4)
	if header[0] != 5 || header[1] != 0 {
		t.Fatalf("SOCKS reply = %#v", header)
	}
	var size int
	switch header[3] {
	case 1:
		size = 4 + 2
	case 4:
		size = 16 + 2
	case 3:
		length := readFull(t, reader, 1)
		return append(append(header, length...), readFull(t, reader, int(length[0])+2)...)
	default:
		t.Fatalf("SOCKS address type = %d", header[3])
	}
	return append(header, readFull(t, reader, size)...)
}

func readFull(t *testing.T, reader io.Reader, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := io.ReadFull(reader, value); err != nil {
		t.Fatal(err)
	}
	return value
}

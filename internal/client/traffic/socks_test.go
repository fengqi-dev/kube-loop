package traffic

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestSOCKSAddressRoundTrip(t *testing.T) {
	tests := []struct {
		host string
		port uint16
	}{
		{"10.244.1.9", 8080},
		{"fd00::10", 53},
		{"api.default.svc.cluster.local", 443},
	}
	for _, test := range tests {
		encoded, err := encodeAddress(test.host, test.port)
		if err != nil {
			t.Fatal(err)
		}
		host, port, err := readAddress(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		wantIP := net.ParseIP(test.host)
		gotIP := net.ParseIP(host)
		if wantIP != nil {
			if gotIP == nil || !wantIP.Equal(gotIP) {
				t.Fatalf("host = %q, want %q", host, test.host)
			}
		} else if host != test.host {
			t.Fatalf("host = %q, want %q", host, test.host)
		}
		if port != test.port {
			t.Fatalf("port = %d, want %d", port, test.port)
		}
	}
}

func TestDecodeSOCKSUDPDatagram(t *testing.T) {
	address, err := encodeAddress("10.96.0.10", 53)
	if err != nil {
		t.Fatal(err)
	}
	packet := append([]byte{0, 0, 0}, address...)
	packet = append(packet, []byte("dns-payload")...)
	payload, err := decodeDatagram(packet)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "dns-payload" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestDecodeSOCKSUDPRejectsFragments(t *testing.T) {
	_, err := decodeDatagram([]byte{0, 0, 1, socksAddressIPv4, 127, 0, 0, 1, 0, 53})
	if err == nil {
		t.Fatal("expected fragmented datagram error")
	}
}

func TestDialTCPWithPasswordAuthentication(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		greeting := make([]byte, 3)
		if _, readErr := io.ReadFull(reader, greeting); readErr != nil {
			serverErr <- readErr
			return
		}
		if err := writeAll(conn, []byte{socksVersion, socksMethodPassword}); err != nil {
			serverErr <- err
			return
		}
		authHeader := make([]byte, 2)
		if _, readErr := io.ReadFull(reader, authHeader); readErr != nil {
			serverErr <- readErr
			return
		}
		username := make([]byte, int(authHeader[1]))
		if _, readErr := io.ReadFull(reader, username); readErr != nil {
			serverErr <- readErr
			return
		}
		var passwordLength [1]byte
		if _, readErr := io.ReadFull(reader, passwordLength[:]); readErr != nil {
			serverErr <- readErr
			return
		}
		password := make([]byte, int(passwordLength[0]))
		if _, readErr := io.ReadFull(reader, password); readErr != nil {
			serverErr <- readErr
			return
		}
		if string(username) != "test-user" || string(password) != "test-password" {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		if err := writeAll(conn, []byte{1, 0}); err != nil {
			serverErr <- err
			return
		}
		requestHeader := make([]byte, 3)
		if _, readErr := io.ReadFull(reader, requestHeader); readErr != nil {
			serverErr <- readErr
			return
		}
		if requestHeader[1] != socksCommandConnect {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		host, port, readErr := readAddress(reader)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		if host != "10.244.1.9" || port != 8080 {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		bound, _ := encodeAddress("127.0.0.1", 12345)
		response := append([]byte{socksVersion, 0, 0}, bound...)
		response = append(response, []byte("ready")...)
		if err := writeAll(conn, response); err != nil {
			serverErr <- err
			return
		}
		payload := make([]byte, 4)
		if _, readErr := io.ReadFull(reader, payload); readErr != nil {
			serverErr <- readErr
			return
		}
		serverErr <- writeAll(conn, payload)
	}()

	dialer := Dialer{Endpoint: Endpoint{
		Address: listener.Addr().String(), Username: "test-user", Password: "test-password",
	}}
	conn, err := dialer.DialContext(context.Background(), "tcp", "10.244.1.9:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	ready := make([]byte, 5)
	if _, err := io.ReadFull(conn, ready); err != nil {
		t.Fatal(err)
	}
	if string(ready) != "ready" {
		t.Fatalf("initial payload = %q", ready)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != "ping" {
		t.Fatalf("echo = %q", echo)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestDialUDPHidesSOCKSDatagramFraming(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErr := make(chan error, 1)
	go func() {
		control, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer control.Close()
		reader := bufio.NewReader(control)
		greeting := make([]byte, 3)
		if _, readErr := io.ReadFull(reader, greeting); readErr != nil {
			serverErr <- readErr
			return
		}
		if err := writeAll(control, []byte{socksVersion, socksMethodNone}); err != nil {
			serverErr <- err
			return
		}
		requestHeader := make([]byte, 3)
		if _, readErr := io.ReadFull(reader, requestHeader); readErr != nil {
			serverErr <- readErr
			return
		}
		if requestHeader[1] != socksCommandUDPAssociate {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		if _, _, readErr := readAddress(reader); readErr != nil {
			serverErr <- readErr
			return
		}
		relay, listenErr := net.ListenUDP("udp", &net.UDPAddr{
			IP: net.ParseIP("127.0.0.1"),
		})
		if listenErr != nil {
			serverErr <- listenErr
			return
		}
		defer relay.Close()
		bound, _ := encodeAddress("127.0.0.1", uint16(relay.LocalAddr().(*net.UDPAddr).Port))
		if err := writeAll(control, append([]byte{socksVersion, 0, 0}, bound...)); err != nil {
			serverErr <- err
			return
		}
		packet := make([]byte, 2048)
		n, client, readErr := relay.ReadFromUDP(packet)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		packetReader := bytes.NewReader(packet[3:n])
		host, port, readErr := readAddress(packetReader)
		if readErr != nil {
			serverErr <- readErr
			return
		}
		payload := packet[n-packetReader.Len() : n]
		if host != "10.96.0.10" || port != 53 || string(payload) != "query" {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		target, _ := encodeAddress(host, port)
		response := append([]byte{0, 0, 0}, target...)
		response = append(response, []byte("response")...)
		_, writeErr := relay.WriteToUDP(response, client)
		serverErr <- writeErr
	}()

	dialer := Dialer{Endpoint: Endpoint{Address: listener.Addr().String()}}
	conn, err := dialer.DialContext(context.Background(), "udp", "10.96.0.10:53")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte("query")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 32)
	n, err := conn.Read(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(response[:n]) != "response" {
		t.Fatalf("response = %q", response[:n])
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

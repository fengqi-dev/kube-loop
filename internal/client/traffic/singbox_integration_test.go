package traffic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDialerInteroperatesWithSingBox(t *testing.T) {
	binary := os.Getenv("KUBELOOP_SINGBOX_PATH")
	if binary == "" {
		t.Skip("KUBELOOP_SINGBOX_PATH is not set")
	}
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		conn, acceptErr := target.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()
	udpTarget, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer udpTarget.Close()
	go func() {
		buffer := make([]byte, 64)
		n, source, readErr := udpTarget.ReadFromUDP(buffer)
		if readErr == nil {
			_, _ = udpTarget.WriteToUDP(buffer[:n], source)
		}
	}()

	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyPort := reservation.Addr().(*net.TCPAddr).Port
	_ = reservation.Close()

	config := map[string]any{
		"log": map[string]any{"disabled": true},
		"inbounds": []map[string]any{{
			"type": "socks", "tag": "traffic-in",
			"listen": "127.0.0.1", "listen_port": proxyPort,
			"users": []map[string]any{{
				"username": "exchange", "password": "integration-password",
			}},
		}},
		"outbounds": []map[string]any{{"type": "direct", "tag": "local"}},
		"route": map[string]any{
			"rules": []map[string]any{{
				"inbound": []string{"traffic-in"}, "auth_user": []string{"exchange"},
				"outbound": "local",
			}},
			"final": "local", "auto_detect_interface": true,
		},
	}
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	command := exec.CommandContext(ctx, binary, "run", "-c", configPath)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
	}()

	dialer := Dialer{Endpoint: Endpoint{
		Address:  net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)),
		Username: "exchange", Password: "integration-password",
	}}
	var connection net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connection, err = dialer.DialContext(
			context.Background(), "tcp", target.Addr().String(),
		)
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect through sing-box: %v\n%s", err, output.String())
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "ping" {
		t.Fatalf("response = %q", response)
	}

	udpConnection, err := dialer.DialContext(
		context.Background(), "udp", udpTarget.LocalAddr().String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConnection.Close()
	_ = udpConnection.SetDeadline(time.Now().Add(time.Second))
	if _, err := udpConnection.Write([]byte("dns")); err != nil {
		t.Fatal(err)
	}
	udpResponse := make([]byte, 3)
	if _, err := io.ReadFull(udpConnection, udpResponse); err != nil {
		t.Fatal(err)
	}
	if string(udpResponse) != "dns" {
		t.Fatalf("UDP response = %q", udpResponse)
	}
}

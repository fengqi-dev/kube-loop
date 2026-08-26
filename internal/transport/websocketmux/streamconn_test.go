package websocketmux

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestStreamConnPreservesBidirectionalHalfClose(t *testing.T) {
	left, right := net.Pipe()
	client := NewStreamConn(left)
	server := NewStreamConn(right)
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	result := make(chan error, 1)
	go func() {
		request, err := io.ReadAll(server)
		if err != nil {
			result <- err
			return
		}
		if string(request) != "request" {
			result <- io.ErrUnexpectedEOF
			return
		}
		if _, err := io.WriteString(server, "response"); err != nil {
			result <- err
			return
		}
		result <- server.CloseWrite()
	}()

	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(client, "request"); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "response" {
		t.Fatalf("response = %q", response)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("after FIN")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after FIN error = %v", err)
	}
}

func TestStreamConnRejectsOversizedFrame(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	go func() {
		_, _ = right.Write([]byte{streamFrameData, 0, 2, 0, 1})
	}()
	buffer := make([]byte, 8)
	if _, err := NewStreamConn(left).Read(buffer); err == nil {
		t.Fatal("expected oversized frame rejection")
	}
}

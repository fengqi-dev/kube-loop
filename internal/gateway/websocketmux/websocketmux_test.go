package websocketmux

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForwarderMultiplexesConcurrentStreams(t *testing.T) {
	handler, err := NewHandler(ServerConfig{
		Token: "test-token",
		Handle: func(connection net.Conn) {
			defer connection.Close()
			_, _ = io.Copy(connection, connection)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(handler.ServeHTTP))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	forwarder, err := Start(ctx, ClientConfig{
		URL:               "ws" + strings.TrimPrefix(server.URL, "http"),
		Token:             "test-token",
		PoolSize:          2,
		MaxStreamsPerConn: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer forwarder.Close()

	const streamCount = 24
	var wait sync.WaitGroup
	errorsCh := make(chan error, streamCount)
	for index := range streamCount {
		wait.Add(1)
		go func() {
			defer wait.Done()
			connection, dialErr := net.DialTimeout("tcp", forwarder.Address(), time.Second)
			if dialErr != nil {
				errorsCh <- dialErr
				return
			}
			defer connection.Close()
			message := fmt.Sprintf("stream-%d\n", index)
			if _, writeErr := io.WriteString(connection, message); writeErr != nil {
				errorsCh <- writeErr
				return
			}
			response, readErr := bufio.NewReader(connection).ReadString('\n')
			if readErr != nil {
				errorsCh <- readErr
				return
			}
			if response != message {
				errorsCh <- fmt.Errorf("response %q, want %q", response, message)
			}
		}()
	}
	wait.Wait()
	close(errorsCh)
	for testErr := range errorsCh {
		t.Error(testErr)
	}
}

func TestForwarderRejectsInvalidToken(t *testing.T) {
	var logs bytes.Buffer
	handler, err := NewHandler(ServerConfig{
		Token: "correct", Logger: log.New(&logs, "", 0), Handle: func(net.Conn) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, err = Start(context.Background(), ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "wrong",
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected HTTP 401, got %v", err)
	}
	if !strings.Contains(logs.String(), "reason=authentication") {
		t.Fatalf("missing authentication rejection log: %q", logs.String())
	}
	if strings.Contains(logs.String(), "correct") || strings.Contains(logs.String(), "wrong") {
		t.Fatalf("authentication log leaked a token: %q", logs.String())
	}
}

func TestForwarderValidatesEndpointAndMultiplexingLimits(t *testing.T) {
	for _, config := range []ClientConfig{
		{URL: "https://gateway.example.com", Token: "token"},
		{URL: "wss://gateway.example.com", Token: "token", PoolSize: 9},
		{URL: "wss://gateway.example.com", Token: "token", MaxPhysical: 17},
		{URL: "wss://gateway.example.com", Token: "token", MaxStreamsPerConn: 1025},
	} {
		if _, err := Start(context.Background(), config); err == nil {
			t.Fatalf("expected validation error for %+v", config)
		}
	}
}

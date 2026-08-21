package trafficstream

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
)

func TestFrameConnRoundTripsExchangeAndMirrorFrames(t *testing.T) {
	writer, reader := mustTrafficPair(t)

	exchange, err := exchangestream.Encode(exchangestream.Frame{
		Type: exchangestream.Data, StreamID: 7, Payload: []byte("exchange"),
	})
	if err != nil {
		t.Fatal(err)
	}
	mirror, err := mirrorstream.Encode(mirrorstream.Frame{
		Type: mirrorstream.Datagram, StreamID: 9, ServicePort: 5353,
		Protocol: mirrorstream.ProtocolUDP, Payload: []byte("mirror"),
	})
	if err != nil {
		t.Fatal(err)
	}

	writeErr := make(chan error, 1)
	go func() {
		if err := writer.WriteFrame(t.Context(), exchange); err != nil {
			writeErr <- err
			return
		}
		writeErr <- writer.WriteFrame(t.Context(), mirror)
	}()
	for _, want := range [][]byte{exchange, mirror} {
		got, err := reader.ReadFrame(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame = %x, want %x", got, want)
		}
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
}

func TestFrameConnRejectsInvalidFrameSizesAndTextMessages(t *testing.T) {
	tests := []struct {
		name        string
		messageType int
		payload     []byte
	}{
		{name: "empty", messageType: websocket.BinaryMessage},
		{name: "oversize", messageType: websocket.BinaryMessage, payload: make([]byte, MaximumFrameBytes+1)},
		{name: "text", messageType: websocket.TextMessage, payload: []byte("not binary")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer, reader := mustTrafficPair(t)
			writeDone := make(chan error, 1)
			go func() {
				writeDone <- writer.conn.WriteMessage(test.messageType, test.payload)
			}()
			if _, err := reader.ReadFrame(t.Context()); err == nil {
				t.Fatal("invalid Traffic WebSocket message was accepted")
			}
			if err := <-writeDone; err != nil && test.name != "oversize" {
				t.Fatal(err)
			}
		})
	}

	writer, _ := mustTrafficPair(t)
	if err := writer.WriteFrame(t.Context(), nil); err == nil {
		t.Fatal("empty frame was accepted")
	}
	if err := writer.WriteFrame(t.Context(), make([]byte, MaximumFrameBytes+1)); err == nil {
		t.Fatal("oversize frame was accepted")
	}
}

func TestFrameConnReadPropagatesContextCancellation(t *testing.T) {
	_, reader := mustTrafficPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	readDone := make(chan error, 1)
	go func() {
		_, err := reader.ReadFrame(ctx)
		readDone <- err
	}()
	cancel()
	select {
	case err := <-readDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled read did not return")
	}
}

func TestFrameConnReadPropagatesContextDeadline(t *testing.T) {
	_, reader := mustTrafficPair(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	readDone := make(chan error, 1)
	go func() {
		_, err := reader.ReadFrame(ctx)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("read error = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline-bound read did not return")
	}
}

func TestFrameConnSerializesConcurrentWrites(t *testing.T) {
	writer, reader := mustTrafficPair(t)
	const frameCount = 32

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	writeErrs := make(chan error, frameCount)
	var writers sync.WaitGroup
	for id := 1; id <= frameCount; id++ {
		writers.Go(func() {
			frame, err := exchangestream.Encode(exchangestream.Frame{
				Type: exchangestream.Data, StreamID: uint64(id),
				Payload: []byte(strconv.Itoa(id)),
			})
			if err == nil {
				err = writer.WriteFrame(ctx, frame)
			}
			writeErrs <- err
		})
	}

	seen := make(map[uint64]struct{}, frameCount)
	for range frameCount {
		raw, err := reader.ReadFrame(ctx)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := exchangestream.Decode(raw)
		if err != nil {
			t.Fatalf("decode concurrently written frame: %v", err)
		}
		seen[frame.StreamID] = struct{}{}
	}
	writers.Wait()
	close(writeErrs)
	for err := range writeErrs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != frameCount {
		t.Fatalf("received %d distinct frames, want %d", len(seen), frameCount)
	}
}

func TestFrameConnRequiresConnectionAndContext(t *testing.T) {
	if _, err := Dial(t.Context(), nil); err == nil {
		t.Fatal("nil Dial connection was accepted")
	}
	if _, err := Accept(t.Context(), nil); err == nil {
		t.Fatal("nil Accept connection was accepted")
	}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	if _, err := Dial(nil, client); err == nil { //nolint:staticcheck // Intentionally verifies nil context rejection.
		t.Fatal("nil Dial context was accepted")
	}
	if _, err := Accept(nil, server); err == nil { //nolint:staticcheck // Intentionally verifies nil context rejection.
		t.Fatal("nil Accept context was accepted")
	}
	framed, _ := mustTrafficPair(t)
	if _, err := framed.ReadFrame(
		nil, //nolint:staticcheck // Intentionally verifies nil context rejection.
	); err == nil {
		t.Fatal("nil read context was accepted")
	}
	if err := framed.WriteFrame(
		nil, //nolint:staticcheck // Intentionally verifies nil context rejection.
		[]byte{1},
	); err == nil {
		t.Fatal("nil write context was accepted")
	}
}

func TestAcceptPropagatesHandshakeCancellation(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	accepted := make(chan error, 1)
	go func() {
		_, err := Accept(ctx, server)
		accepted <- err
	}()
	cancel()
	select {
	case err := <-accepted:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Accept error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled Traffic WebSocket handshake did not return")
	}
}

func mustTrafficPair(t *testing.T) (*FrameConn, *FrameConn) {
	t.Helper()
	clientConnection, serverConnection := net.Pipe()
	acceptResult := make(chan struct {
		connection *FrameConn
		err        error
	}, 1)
	go func() {
		connection, err := Accept(t.Context(), serverConnection)
		acceptResult <- struct {
			connection *FrameConn
			err        error
		}{connection: connection, err: err}
	}()
	client, err := Dial(t.Context(), clientConnection)
	if err != nil {
		_ = clientConnection.Close()
		_ = serverConnection.Close()
		t.Fatal(err)
	}
	accepted := <-acceptResult
	if accepted.err != nil {
		_ = client.Close()
		_ = serverConnection.Close()
		t.Fatal(accepted.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = accepted.connection.Close()
	})
	return client, accepted.connection
}

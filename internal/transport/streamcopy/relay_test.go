package streamcopy

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// tcpPair returns a real TCP connection pair. net.Pipe is unusable here: it is
// unbuffered and cannot represent a half-close, so CloseWrite would fall back
// to tearing down the whole pipe before the peer has read anything.
func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	type accepted struct {
		conn net.Conn
		err  error
	}
	incoming := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		incoming <- accepted{conn: conn, err: err}
	}()

	dialed, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	result := <-incoming
	if result.err != nil {
		t.Fatalf("accept: %v", result.err)
	}
	t.Cleanup(func() {
		_ = dialed.Close()
		_ = result.conn.Close()
	})
	return dialed.(*net.TCPConn), result.conn.(*net.TCPConn)
}

func TestBidirectionalCopiesBothDirections(t *testing.T) {
	leftPeer, leftRelay := tcpPair(t)
	rightRelay, rightPeer := tcpPair(t)

	finished := make(chan Result, 1)
	go func() { finished <- Bidirectional(leftRelay, rightRelay) }()

	if _, err := leftPeer.Write([]byte("ping")); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	if err := leftPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close left peer: %v", err)
	}
	received := make([]byte, 4)
	if _, err := io.ReadFull(rightPeer, received); err != nil {
		t.Fatalf("read right peer: %v", err)
	}
	if string(received) != "ping" {
		t.Fatalf("right peer received %q, want %q", received, "ping")
	}

	if _, err := rightPeer.Write([]byte("pong!!")); err != nil {
		t.Fatalf("write pong: %v", err)
	}
	if err := rightPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close right peer: %v", err)
	}
	answered := make([]byte, 6)
	if _, err := io.ReadFull(leftPeer, answered); err != nil {
		t.Fatalf("read left peer: %v", err)
	}
	if string(answered) != "pong!!" {
		t.Fatalf("left peer received %q, want %q", answered, "pong!!")
	}

	select {
	case result := <-finished:
		if result.LeftToRight != 4 {
			t.Fatalf("LeftToRight = %d, want 4", result.LeftToRight)
		}
		if result.RightToLeft != 6 {
			t.Fatalf("RightToLeft = %d, want 6", result.RightToLeft)
		}
		if result.Err != nil {
			t.Fatalf("Err = %v, want nil", result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish")
	}
}

func TestRelayKeepsReverseDirectionOpenAfterHalfClose(t *testing.T) {
	leftPeer, leftRelay := tcpPair(t)
	rightRelay, rightPeer := tcpPair(t)

	finished := make(chan Result, 1)
	go func() { finished <- Bidirectional(leftRelay, rightRelay) }()

	// The left peer says its piece and half-closes. The reverse direction must
	// still carry the response back.
	if _, err := leftPeer.Write([]byte("request")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := leftPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close left peer: %v", err)
	}

	request := make([]byte, 7)
	if _, err := io.ReadFull(rightPeer, request); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if _, err := rightPeer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("right peer read after half-close = %v, want EOF", err)
	}

	if _, err := rightPeer.Write([]byte("response")); err != nil {
		t.Fatalf("write response: %v", err)
	}
	response := make([]byte, 8)
	if _, err := io.ReadFull(leftPeer, response); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(response) != "response" {
		t.Fatalf("left peer received %q, want %q", response, "response")
	}

	_ = rightPeer.Close()
	select {
	case result := <-finished:
		if result.LeftToRight != 7 || result.RightToLeft != 8 {
			t.Fatalf("result = %+v, want 7 and 8 bytes", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish after both directions ended")
	}
}

func TestRelaySplitHalvesCopyThroughBufferedReader(t *testing.T) {
	clientPeer, clientRelay := tcpPair(t)
	targetRelay, targetPeer := tcpPair(t)

	// The SOCKS bridge reads through a buffer that already holds part of the
	// stream, so the read and write halves are distinct values.
	buffered := io.MultiReader(strings.NewReader("buffered"), clientRelay)

	finished := make(chan Result, 1)
	go func() {
		finished <- Relay(
			Side{Reader: buffered, Writer: clientRelay},
			ConnSide(targetRelay),
			Options{},
		)
	}()

	if _, err := clientPeer.Write([]byte("+live")); err != nil {
		t.Fatalf("write live bytes: %v", err)
	}
	if err := clientPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close client peer: %v", err)
	}

	forwarded, err := io.ReadAll(targetPeer)
	if err != nil {
		t.Fatalf("read target peer: %v", err)
	}
	if string(forwarded) != "buffered+live" {
		t.Fatalf("target received %q, want %q", forwarded, "buffered+live")
	}

	if err := targetPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close target peer: %v", err)
	}
	select {
	case result := <-finished:
		if result.LeftToRight != int64(len("buffered+live")) {
			t.Fatalf("LeftToRight = %d, want %d", result.LeftToRight, len("buffered+live"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish")
	}
}

func TestRelayHalfCloseTimeoutUnblocksStalledDirection(t *testing.T) {
	leftPeer, leftRelay := tcpPair(t)
	rightRelay, rightPeer := tcpPair(t)

	finished := make(chan Result, 1)
	go func() {
		finished <- Relay(
			ConnSide(leftRelay),
			ConnSide(rightRelay),
			Options{HalfCloseTimeout: 100 * time.Millisecond},
		)
	}()

	// The left peer half-closes, then the right peer goes silent forever
	// without ever closing. Without the timeout the relay would hang here.
	if err := leftPeer.CloseWrite(); err != nil {
		t.Fatalf("half-close left peer: %v", err)
	}

	select {
	case result := <-finished:
		if !errors.Is(result.Err, ErrHalfCloseTimeout) {
			t.Fatalf("Err = %v, want ErrHalfCloseTimeout", result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay ignored HalfCloseTimeout")
	}

	// Both sides are closed outright so the stalled copy can drain.
	if _, err := rightPeer.Read(make([]byte, 1)); err == nil {
		t.Fatal("right peer read succeeded, want failure after forced close")
	}
}

func TestCloseWritePrefersHalfCloseOverFullClose(t *testing.T) {
	half := &recordingCloser{}
	CloseWrite(half)
	if !half.wroteClosed {
		t.Fatal("CloseWrite did not use CloseWrite")
	}
	if half.fullyClosed {
		t.Fatal("CloseWrite fully closed a half-closable value")
	}

	full := &plainCloser{}
	CloseWrite(full)
	if !full.closed {
		t.Fatal("CloseWrite did not fall back to Close")
	}

	// A value with neither half must not panic.
	CloseWrite(struct{}{})
}

type recordingCloser struct {
	wroteClosed bool
	fullyClosed bool
}

func (closer *recordingCloser) Write(payload []byte) (int, error) { return len(payload), nil }
func (closer *recordingCloser) CloseWrite() error                 { closer.wroteClosed = true; return nil }
func (closer *recordingCloser) Close() error                      { closer.fullyClosed = true; return nil }

type plainCloser struct{ closed bool }

func (closer *plainCloser) Write(payload []byte) (int, error) { return len(payload), nil }
func (closer *plainCloser) Close() error                      { closer.closed = true; return nil }

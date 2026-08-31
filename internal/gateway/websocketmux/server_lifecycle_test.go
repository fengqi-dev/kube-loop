package websocketmux

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
)

type blockingListener struct {
	accepted   chan struct{}
	closed     chan struct{}
	acceptOnce sync.Once
	closeOnce  sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{accepted: make(chan struct{}), closed: make(chan struct{})}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	listener.acceptOnce.Do(func() { close(listener.accepted) })
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *blockingListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (*blockingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

func TestServeStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	listener := newBlockingListener()
	result := make(chan error, 1)
	go func() {
		result <- Serve(ctx, listener, http.NotFoundHandler())
	}()

	<-listener.accepted
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Serve() after cancellation: %v", err)
	}
}

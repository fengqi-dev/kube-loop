package listener

import (
	"context"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
)

type routedForwarder struct {
	listener net.Listener
	target   string
	dialer   TrafficDialer
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
	connMu   sync.Mutex
	conns    map[net.Conn]struct{}
}

var _ Forwarder = (*routedForwarder)(nil)

func newRoutedForwarder(
	listener net.Listener, target string, dialer TrafficDialer,
) *routedForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	forwarder := &routedForwarder{
		listener: listener, target: target, dialer: dialer, ctx: ctx, cancel: cancel,
		conns: make(map[net.Conn]struct{}),
	}
	forwarder.wg.Go(forwarder.serve)
	return forwarder
}

func (f *routedForwarder) Address() string { return f.listener.Addr().String() }

func (f *routedForwarder) Close() error {
	var err error
	f.once.Do(func() {
		f.cancel()
		err = f.listener.Close()
		f.connMu.Lock()
		for conn := range f.conns {
			_ = conn.Close()
		}
		f.connMu.Unlock()
		f.wg.Wait()
	})
	return err
}

func (f *routedForwarder) serve() {
	for {
		client, err := f.listener.Accept()
		if err != nil {
			return
		}
		if !f.track(client) {
			continue
		}
		f.wg.Go(func() {
			defer f.untrack(client)
			f.forward(client)
		})
	}
}

func (f *routedForwarder) forward(client net.Conn) {
	defer client.Close()
	target, err := f.dialer.DialContext(f.ctx, "tcp", f.target)
	if err != nil {
		return
	}
	if !f.track(target) {
		return
	}
	defer f.untrack(target)
	defer target.Close()
	streamcopy.Bidirectional(client, target)
}

func (f *routedForwarder) track(conn net.Conn) bool {
	f.connMu.Lock()
	defer f.connMu.Unlock()
	if f.ctx.Err() != nil {
		_ = conn.Close()
		return false
	}
	f.conns[conn] = struct{}{}
	return true
}

func (f *routedForwarder) untrack(conn net.Conn) {
	f.connMu.Lock()
	delete(f.conns, conn)
	f.connMu.Unlock()
}

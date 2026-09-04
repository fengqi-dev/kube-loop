package websocketmux

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/xtaci/smux"

	"github.com/fengqi-dev/kube-loop/internal/transport/streamcopy"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/transport/websocketmux"
)

// OpenStream opens one tracked logical connection on the existing WebSocket
// pool. Closing the Forwarder (including during Data Plane recovery) closes
// every stream handed out here.
func (forwarder *Forwarder) OpenStream(ctx context.Context) (net.Conn, error) {
	if ctx == nil {
		return nil, errors.New("gateway logical stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-forwarder.ctx.Done():
		return nil, net.ErrClosed
	case <-forwarder.openGate:
	}
	defer func() { forwarder.openGate <- struct{}{} }()

	stream, err := forwarder.openStream()
	if err != nil {
		return nil, err
	}
	connection := &trackedConn{Conn: protocolmux.NewStreamConn(stream)}
	connection.onClose = func() {
		forwarder.mu.Lock()
		delete(forwarder.streams, connection)
		forwarder.mu.Unlock()
	}
	forwarder.mu.Lock()
	if forwarder.closed {
		forwarder.mu.Unlock()
		_ = connection.Close()
		return nil, net.ErrClosed
	}
	forwarder.streams[connection] = struct{}{}
	forwarder.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

// openStream picks a healthy pooled session, dialing a new one if the current
// pool cannot serve the stream, and retries once.
func (forwarder *Forwarder) openStream() (*smux.Stream, error) {
	for attempt := range 2 {
		item := forwarder.pickSession()
		if item != nil {
			stream, err := item.session.OpenStream()
			if err == nil {
				return stream, nil
			}
			forwarder.discard(item)
		}
		if _, err := forwarder.ensureSession(); err != nil && attempt == 1 {
			return nil, err
		}
	}
	return nil, errors.New("no healthy Gateway WebSocket session")
}

type trackedConn struct {
	net.Conn

	onClose func()
	once    sync.Once
	err     error
}

func (connection *trackedConn) Close() error {
	connection.once.Do(func() {
		connection.err = connection.Conn.Close()
		if connection.onClose != nil {
			connection.onClose()
		}
	})
	return connection.err
}

func (forwarder *Forwarder) acceptLoop() {
	for {
		connection, err := forwarder.listener.Accept()
		if err != nil {
			return
		}
		forwarder.mu.Lock()
		if forwarder.closed {
			forwarder.mu.Unlock()
			_ = connection.Close()
			continue
		}
		forwarder.locals[connection] = struct{}{}
		forwarder.mu.Unlock()
		forwarder.wg.Go(func() {
			defer func() {
				forwarder.mu.Lock()
				defer forwarder.mu.Unlock()
				delete(forwarder.locals, connection)
			}()
			forwarder.forward(connection)
		})
	}
}

func (forwarder *Forwarder) forward(local net.Conn) {
	stream, err := forwarder.openStream()
	if err != nil {
		_ = local.Close()
		return
	}
	defer func() {
		_ = stream.Close() // Closing only terminates the failed forwarding path; no caller can act on the result.
	}()
	defer func() {
		_ = local.Close() // Closing only terminates the failed forwarding path; no caller can act on the result.
	}()
	streamcopy.Bidirectional(local, protocolmux.NewStreamConn(stream))
}

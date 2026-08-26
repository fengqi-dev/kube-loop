package websocketmux

import (
	"context"
	"errors"
	"net"

	"github.com/xtaci/smux"

	protocolmux "github.com/fengqi-dev/kube-loop/internal/transport/websocketmux"
)

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

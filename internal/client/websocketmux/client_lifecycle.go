package websocketmux

import (
	"net"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/protocol/websocketmux"
)

func (forwarder *Forwarder) Address() string { return forwarder.listener.Addr().String() }

func (forwarder *Forwarder) Close() error {
	forwarder.closeOnce.Do(func() {
		forwarder.cancel()
		forwarder.closeErr = forwarder.listener.Close()
		forwarder.mu.Lock()
		forwarder.closed = true
		sessions := append([]*pooledSession(nil), forwarder.sessions...)
		forwarder.sessions = nil
		locals := make([]net.Conn, 0, len(forwarder.locals))
		for connection := range forwarder.locals {
			locals = append(locals, connection)
		}
		forwarder.locals = make(map[net.Conn]struct{})
		streams := make([]net.Conn, 0, len(forwarder.streams))
		for connection := range forwarder.streams {
			streams = append(streams, connection)
		}
		forwarder.streams = make(map[net.Conn]struct{})
		forwarder.mu.Unlock()
		for _, connection := range locals {
			_ = connection.Close()
		}
		for _, connection := range streams {
			_ = connection.Close()
		}
		for _, item := range sessions {
			_ = item.session.Close()
			_ = closeWebSocket(item.ws, websocket.CloseNormalClosure, "client shutdown")
		}
		forwarder.wg.Wait()
	})
	return forwarder.closeErr
}

// OpenStream opens one tracked logical connection on the existing WebSocket
// pool. Closing the Forwarder (including during Data Plane recovery) closes
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
	defer forwarder.wg.Done()
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
		forwarder.wg.Add(1)
		forwarder.mu.Unlock()
		go func() {
			defer forwarder.wg.Done()
			defer func() {
				forwarder.mu.Lock()
				delete(forwarder.locals, connection)
				forwarder.mu.Unlock()
			}()
			forwarder.forward(connection)
		}()
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

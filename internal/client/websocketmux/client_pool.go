package websocketmux

import (
	"context"
	"errors"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/gorilla/websocket"
)

func (forwarder *Forwarder) pickSession() *pooledSession {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	var selected *pooledSession
	for _, item := range forwarder.sessions {
		if item.session.IsClosed() || item.session.NumStreams() >= item.maxStreams {
			continue
		}
		if selected == nil || item.session.NumStreams() < selected.session.NumStreams() {
			selected = item
		}
	}
	return selected
}

func (forwarder *Forwarder) ensureSession() (*pooledSession, error) {
	forwarder.dialMu.Lock()
	defer forwarder.dialMu.Unlock()
	if item := forwarder.pickSession(); item != nil {
		return item, nil
	}
	forwarder.mu.Lock()
	count := len(forwarder.sessions)
	forwarder.mu.Unlock()
	forwarder.mu.Lock()
	maximum := forwarder.maxPhysical
	forwarder.mu.Unlock()
	if count >= maximum {
		return nil, errors.New("all Gateway WebSocket sessions are at capacity")
	}
	item, err := forwarder.dial()
	if err != nil {
		return nil, err
	}
	if !forwarder.commitSession(item) {
		_ = item.session.Close()
		_ = closeWebSocket(item.ws, websocket.CloseGoingAway, "client closed during session setup")
		return nil, net.ErrClosed
	}
	return item, nil
}

func (forwarder *Forwarder) commitSession(item *pooledSession) bool {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	if forwarder.closed {
		return false
	}
	forwarder.sessions = append(forwarder.sessions, item)
	forwarder.wg.Go(func() {
		forwarder.keepAlive(item)
	})
	return true
}

func (forwarder *Forwarder) discard(target *pooledSession) {
	forwarder.mu.Lock()
	for index, item := range forwarder.sessions {
		if item == target {
			forwarder.sessions = append(forwarder.sessions[:index], forwarder.sessions[index+1:]...)
			break
		}
	}
	forwarder.mu.Unlock()
	ctx := middleware.WithID(context.Background(), target.correlationID)
	forwarder.logger.InfoContext(
		ctx, "Gateway WebSocket session closed",
		"operation", "gateway.websocket.session",
		"outcome", "closed",
		"correlation_id", target.correlationID,
		"session_id", forwarder.config.SessionID,
		"session_generation", forwarder.config.SessionGeneration,
	)
	_ = target.session.Close()
	_ = closeWebSocket(target.ws, websocket.CloseGoingAway, "session replaced")
}

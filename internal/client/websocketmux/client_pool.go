package websocketmux

import (
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
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
	forwarder.mu.Lock()
	forwarder.sessions = append(forwarder.sessions, item)
	forwarder.mu.Unlock()
	return item, nil
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
	_ = target.session.Close()
	_ = target.ws.Close(websocket.StatusGoingAway, "session replaced")
}

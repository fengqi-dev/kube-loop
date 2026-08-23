package websocketmux

import (
	"context"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

func (h *Handler) acquireGeneration(identity Identity) bool {
	h.generationMu.Lock()
	defer h.generationMu.Unlock()
	current := h.generations[identity.SessionID]
	if identity.SessionGeneration < current.latest {
		return false
	}
	if identity.SessionGeneration > current.latest {
		current.latest = identity.SessionGeneration
	}
	current.sessions++
	h.generations[identity.SessionID] = current
	return true
}

func (h *Handler) reject(
	parent context.Context,
	requestID string,
	connection *websocket.Conn,
	rejection wssprotocol.Reject,
) {
	h.logf(requestID, "WebSocket handshake rejected: reason=%s", rejection.Code)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Second)
	_ = wssprotocol.Write(ctx, connection, rejection)
	cancel()
	_ = connection.Close(websocket.StatusPolicyViolation, rejection.Code)
}

func (h *Handler) acquireUser(identity Identity) bool {
	key := identity.IdentityID
	h.userMu.Lock()
	defer h.userMu.Unlock()
	if h.userSessions[key] >= h.config.MaxSessionsPerUser {
		return false
	}
	h.userSessions[key]++
	return true
}

func (h *Handler) releaseUser(identity Identity) {
	key := identity.IdentityID
	h.userMu.Lock()
	defer h.userMu.Unlock()
	if h.userSessions[key] <= 1 {
		delete(h.userSessions, key)
		return
	}
	h.userSessions[key]--
}

func (h *Handler) releaseGeneration(identity Identity) {
	h.generationMu.Lock()
	defer h.generationMu.Unlock()
	current := h.generations[identity.SessionID]
	current.sessions--
	if current.sessions <= 0 {
		delete(h.generations, identity.SessionID)
		return
	}
	h.generations[identity.SessionID] = current
}

func (h *Handler) generationCurrent(identity Identity) bool {
	h.generationMu.Lock()
	defer h.generationMu.Unlock()
	return identity.SessionGeneration >= h.generations[identity.SessionID].latest
}

package websocketmux

import (
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wss"
)

func closeWebSocket(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.Close())
}

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
	rejection wss.Reject,
) {
	h.logf(parent, requestID, "WebSocket handshake rejected: reason=%s", rejection.Code)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Second)
	_ = wss.Write(ctx, connection, rejection)
	cancel()
	_ = closeWebSocket(connection, websocket.ClosePolicyViolation, rejection.Code)
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

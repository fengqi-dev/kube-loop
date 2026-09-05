// Package trojanws defines the KubeLoop v3 binding between an authenticated
// WebSocket RelayTicket and the Trojan stream carried by that WebSocket.
package trojanws

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const passwordDomain = "kubeloop/trojan-wss/v3/password\x00"
const sessionPasswordDomain = "kubeloop/trojan-wss/v3/session-password\x00"
const DefaultPath = "/tunnel"

// DerivePassword returns the Trojan password bound to relayTicket.
//
// RelayTicket is already a high-entropy bearer credential. SHA-256 is used for
// domain separation and fixed-size encoding, not for password stretching. The
// caller must protect the result exactly as it protects the original ticket and
// must never persist or log either value.
func DerivePassword(ticket string) (string, error) {
	if ticket == "" || len(ticket) > relayticket.MaximumTicketBytes ||
		strings.TrimSpace(ticket) != ticket || utils.ContainsControl(ticket) {
		return "", errors.New("invalid RelayTicket for Trojan credential")
	}
	digest := sha256.Sum256([]byte(passwordDomain + ticket))
	return hex.EncodeToString(digest[:]), nil
}

// DeriveSessionPassword returns the stable Trojan framing credential for one
// Session generation. It is not the public authentication credential: the
// Gateway validates a fresh RelayTicket before proxying every outer WebSocket
// to its loopback-only sing-box runtime.
func DeriveSessionPassword(sessionID string, generation uint64) (string, error) {
	token, err := tunnel.RelaySessionToken(sessionID, generation)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(sessionPasswordDomain + hex.EncodeToString(token[:])))
	return hex.EncodeToString(digest[:]), nil
}

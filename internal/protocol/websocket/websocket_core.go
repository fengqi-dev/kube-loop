// Package websocket provides the repository's context-aware WebSocket API on
// top of github.com/gorilla/websocket.
package websocket

import (
	"net/http"
	"sync"

	gorilla "github.com/gorilla/websocket"
)

type MessageType = int

const (
	MessageText   MessageType = gorilla.TextMessage
	MessageBinary MessageType = gorilla.BinaryMessage
)

type StatusCode = int

const (
	StatusNormalClosure           StatusCode = gorilla.CloseNormalClosure
	StatusGoingAway               StatusCode = gorilla.CloseGoingAway
	StatusProtocolError           StatusCode = gorilla.CloseProtocolError
	StatusUnsupportedData         StatusCode = gorilla.CloseUnsupportedData
	StatusNoStatusRcvd            StatusCode = gorilla.CloseNoStatusReceived
	StatusAbnormalClosure         StatusCode = gorilla.CloseAbnormalClosure
	StatusInvalidFramePayloadData StatusCode = gorilla.CloseInvalidFramePayloadData
	StatusPolicyViolation         StatusCode = gorilla.ClosePolicyViolation
	StatusMessageTooBig           StatusCode = gorilla.CloseMessageTooBig
	StatusMandatoryExtension      StatusCode = gorilla.CloseMandatoryExtension
	StatusInternalError           StatusCode = gorilla.CloseInternalServerErr
	StatusServiceRestart          StatusCode = gorilla.CloseServiceRestart
	StatusTryAgainLater           StatusCode = gorilla.CloseTryAgainLater
	StatusBadGateway              StatusCode = 1014
	StatusTLSHandshake            StatusCode = gorilla.CloseTLSHandshake
)

type CompressionMode int

const (
	CompressionDisabled CompressionMode = iota
	CompressionContextTakeover
	CompressionNoContextTakeover
)

type DialOptions struct {
	HTTPClient           *http.Client
	HTTPHeader           http.Header
	Host                 string
	Subprotocols         []string
	CompressionMode      CompressionMode
	CompressionThreshold int
}

type AcceptOptions struct {
	Subprotocols         []string
	InsecureSkipVerify   bool
	OriginPatterns       []string
	CompressionMode      CompressionMode
	CompressionThreshold int
}

// Conn serializes Gorilla writes while retaining its single-reader contract.
type Conn struct {
	raw *gorilla.Conn

	readMu  sync.Mutex
	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
}

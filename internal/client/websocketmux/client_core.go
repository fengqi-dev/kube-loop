package websocketmux

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

const Subprotocol = wssprotocol.Subprotocol

const (
	defaultPoolSize          = 2
	defaultMaxPhysical       = 4
	defaultMaxStreams        = 128
	defaultKeepAliveInterval = 15 * time.Second
	defaultKeepAliveTimeout  = 45 * time.Second
	maxPoolSize              = 8
	maxPhysicalConnections   = 16
	maxStreamsPerConnection  = 1024
)

type ClientConfig struct {
	URL               string
	Token             string
	TokenSource       func(context.Context) (string, error)
	TLSConfig         *tls.Config
	ClientVersion     string
	DeviceID          string
	SupportedVersions []string
	HandshakeTimeout  time.Duration
	PoolSize          int
	MaxPhysical       int
	MaxStreamsPerConn int
}

type pooledSession struct {
	ws         *websocket.Conn
	session    *smux.Session
	maxStreams int
}

// HandshakeError is returned when the Gateway explicitly rejects WSS v2
// negotiation. Code is stable and can be mapped to a client upgrade or
// re-authentication action without parsing human-readable text.
type HandshakeError struct {
	Code              string
	Message           string
	SupportedVersions []string
}

func (err *HandshakeError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message == "" {
		return "Gateway rejected WSS handshake: " + err.Code
	}
	return "Gateway rejected WSS handshake: " + err.Code + ": " + err.Message
}

// Forwarder exposes a loopback TCP endpoint and maps each accepted connection
// to an independent smux stream over a small pool of WebSocket connections.
type Forwarder struct {
	ctx      context.Context
	cancel   context.CancelFunc
	listener net.Listener
	config   ClientConfig

	mu          sync.Mutex
	sessions    []*pooledSession
	maxPhysical int
	locals      map[net.Conn]struct{}
	streams     map[net.Conn]struct{}
	closed      bool
	dialMu      sync.Mutex
	openGate    chan struct{}
	closeOnce   sync.Once
	closeErr    error
	wg          sync.WaitGroup
}

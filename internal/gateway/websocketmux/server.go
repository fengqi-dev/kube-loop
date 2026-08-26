package websocketmux

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wss"
)

type Identity struct {
	RequestID         string
	IdentityID        string
	Groups            []string
	DeviceID          string
	SessionID         string
	SessionGeneration uint64
	TicketID          string
	Namespace         string
	NetworkSpecHash   string
	ExpiresAt         time.Time
}

type Authenticator interface {
	Authenticate(*http.Request) (Identity, error)
}

type AuthenticatorFunc func(*http.Request) (Identity, error)

func (function AuthenticatorFunc) Authenticate(request *http.Request) (Identity, error) {
	return function(request)
}

type ServerConfig struct {
	Authenticator        Authenticator
	MaxSessions          int
	MaxStreamsPerSession int
	MaxSessionsPerUser   int
	MaxFrameBytes        int64
	StreamIdleTimeout    time.Duration
	HandshakeTimeout     time.Duration
	ServerVersion        string
	MinClientVersion     string
	SupportedVersions    []string
	Logger               *slog.Logger
	Handle               func(context.Context, Identity, net.Conn)
}

type Handler struct {
	config       ServerConfig
	limit        chan struct{}
	draining     atomic.Bool
	generationMu sync.Mutex
	generations  map[string]activeGeneration
	userMu       sync.Mutex
	userSessions map[string]int
}

type activeGeneration struct {
	latest   uint64
	sessions int
}

func NewHandler(config ServerConfig) (*Handler, error) {
	if config.Authenticator == nil {
		return nil, errors.New("gateway WebSocket authenticator is required")
	}
	if config.Handle == nil {
		return nil, errors.New("gateway stream handler is required")
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = 256
	}
	if config.MaxStreamsPerSession <= 0 {
		config.MaxStreamsPerSession = defaultMaxStreams
	}
	if config.MaxSessionsPerUser <= 0 {
		config.MaxSessionsPerUser = 8
	}
	if config.MaxSessionsPerUser > config.MaxSessions {
		return nil, errors.New("gateway per-user session limit exceeds the global limit")
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = 1 << 20
	}
	if config.MaxFrameBytes < wss.MaximumHandshakeBytes || config.MaxFrameBytes > 16<<20 {
		return nil, errors.New("gateway WebSocket frame limit is invalid")
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = wss.DefaultHandshakeTimeout
	}
	if config.HandshakeTimeout > time.Minute {
		return nil, errors.New("gateway WSS handshake timeout must not exceed one minute")
	}
	if len(config.SupportedVersions) == 0 {
		config.SupportedVersions = []string{wss.Version}
	}
	if len(config.SupportedVersions) != 1 || config.SupportedVersions[0] != wss.Version {
		return nil, errors.New("gateway WSS protocol versions are invalid")
	}
	if config.StreamIdleTimeout <= 0 {
		config.StreamIdleTimeout = defaultStreamIdleTimeout
	}
	if config.StreamIdleTimeout > maxStreamIdleTimeout {
		return nil, errors.New("gateway stream idle timeout must not exceed 24 hours")
	}
	return &Handler{
		config: config, limit: make(chan struct{}, config.MaxSessions),
		generations: make(map[string]activeGeneration), userSessions: make(map[string]int),
	}, nil
}

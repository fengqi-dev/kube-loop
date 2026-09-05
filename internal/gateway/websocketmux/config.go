package websocketmux

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wss"
	shared "github.com/fengqi-dev/kube-loop/internal/transport/websocketmux"
)

const (
	Subprotocol = wss.Subprotocol
	DefaultPath = "/tunnel"
)

const (
	defaultMaxStreams        = 128
	defaultKeepAliveInterval = shared.KeepAliveInterval
	defaultKeepAliveTimeout  = shared.KeepAliveTimeout
	defaultStreamIdleTimeout = 30 * time.Minute
	maxStreamIdleTimeout     = 24 * time.Hour
)

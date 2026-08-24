package websocketmux

import (
	"time"

	"github.com/xtaci/smux"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

const (
	Subprotocol = wssprotocol.Subprotocol
	DefaultPath = "/v1/tunnel"
)

func smuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.Version = 2
	config.KeepAliveInterval = defaultKeepAliveInterval
	config.KeepAliveTimeout = defaultKeepAliveTimeout
	config.MaxReceiveBuffer = 4 * 1024 * 1024
	config.MaxStreamBuffer = 512 * 1024
	return config
}

const (
	defaultMaxStreams        = 128
	defaultKeepAliveInterval = 15 * time.Second
	defaultKeepAliveTimeout  = 45 * time.Second
	defaultStreamIdleTimeout = 30 * time.Minute
	maxStreamIdleTimeout     = 24 * time.Hour
)

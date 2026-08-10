package websocketmux

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
	"github.com/xtaci/smux"
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
	defaultPoolSize          = 2
	defaultMaxPhysical       = 4
	defaultMaxStreams        = 128
	defaultKeepAliveInterval = 15 * time.Second
	defaultKeepAliveTimeout  = 45 * time.Second
	defaultStreamIdleTimeout = 30 * time.Minute
	maxStreamIdleTimeout     = 24 * time.Hour
	maxPoolSize              = 8
	maxPhysicalConnections   = 16
	maxStreamsPerConnection  = 1024
)

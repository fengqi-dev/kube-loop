package websocketmux

import (
	"time"

	"github.com/xtaci/smux"
)

// KeepAliveInterval and KeepAliveTimeout must agree on both ends of a tunnel:
// smux tears the session down when a peer's keepalive is not answered inside
// the timeout, so the client and the Gateway share these values rather than
// declaring them apart.
const (
	KeepAliveInterval = 15 * time.Second
	KeepAliveTimeout  = 45 * time.Second
)

const (
	maxReceiveBuffer = 4 * 1024 * 1024
	maxStreamBuffer  = 512 * 1024
)

// SmuxConfig builds the session settings both tunnel ends must agree on.
// Version 2 is required for the per-stream flow control the buffer sizes below
// configure.
func SmuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.Version = 2
	config.KeepAliveInterval = KeepAliveInterval
	config.KeepAliveTimeout = KeepAliveTimeout
	config.MaxReceiveBuffer = maxReceiveBuffer
	config.MaxStreamBuffer = maxStreamBuffer
	return config
}

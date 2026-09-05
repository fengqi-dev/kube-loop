package dataplane

import (
	"context"
	"log/slog"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
)

func normalizedConfig(config Config) Config {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	config.ListenAddress = strings.TrimSpace(config.ListenAddress)
	if config.ListenAddress == "" {
		config.ListenAddress = DefaultListenAddress
	}
	if config.StartTimeout <= 0 {
		config.StartTimeout = DefaultStartTimeout
	}
	if config.TLSConfig != nil {
		config.TLSConfig = config.TLSConfig.Clone()
	}
	if config.startForwarder == nil {
		config.startForwarder = func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			return websocketmux.Start(ctx, clientConfig)
		}
	}
	if config.listenSOCKS == nil {
		config.listenSOCKS = func(ctx context.Context, listenAddress string) (localBridge, error) {
			return socksbridge.Listen(ctx, listenAddress)
		}
	}
	if config.dialContext == nil {
		config.dialContext = (&net.Dialer{}).DialContext
	}
	return config
}

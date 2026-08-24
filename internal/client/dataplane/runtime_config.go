package dataplane

import (
	"context"
	"log/slog"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
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
	if config.TrafficInspection.TLSConfig != nil {
		config.TrafficInspection.TLSConfig = config.TrafficInspection.TLSConfig.Clone()
	}
	if config.startForwarder == nil {
		config.startForwarder = func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			return websocketmux.Start(ctx, clientConfig)
		}
	}
	if config.listenSOCKS == nil {
		inspection := config.TrafficInspection
		config.listenSOCKS = func(
			ctx context.Context,
			gatewayAddress, listenAddress string,
			token tunnel.SessionToken,
		) (localBridge, error) {
			if !inspection.Enabled && inspection.IsEnabled == nil {
				return socksbridge.Listen(ctx, gatewayAddress, listenAddress, token)
			}
			return socksbridge.Listen(
				ctx,
				gatewayAddress,
				listenAddress,
				token,
				socksbridge.WithTCPInspector(
					func(dialContext socksbridge.DialContextFunc) (socksbridge.TCPInspector, error) {
						authorityPath := strings.TrimSpace(inspection.AuthorityPath)
						if authorityPath == "" {
							var err error
							authorityPath, err = trafficinspect.DefaultAuthorityPath()
							if err != nil {
								return nil, err
							}
						}
						authority, err := trafficinspect.LoadOrCreateAuthority(authorityPath)
						if err != nil {
							return nil, err
						}
						return trafficinspect.New(trafficinspect.Config{
							CA:          authority.TLSCertificate(),
							DialContext: trafficinspect.DialContextFunc(dialContext),
							Enabled:     inspection.IsEnabled,
							OnRequest:   inspection.OnRequest,
							OnResponse:  inspection.OnResponse,
							Sink:        inspection.Sink,
							Policy:      inspection.Policy,
							Protobuf:    inspection.Protobuf,
							OnSinkError: inspection.OnSinkError,
							TLSConfig:   inspection.TLSConfig,
							AllowHTTP2:  true,
						})
					},
				),
			)
		}
	}
	if config.dialContext == nil {
		config.dialContext = (&net.Dialer{}).DialContext
	}
	return config
}

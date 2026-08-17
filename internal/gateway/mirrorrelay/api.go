package mirrorrelay

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

var ErrClientStopped = errClientStopped

type Config struct {
	Now                func() time.Time
	UDPIdleTimeout     time.Duration
	PrimaryDialTimeout time.Duration
	PrimaryDialContext func(context.Context, string, string) (net.Conn, error)
	ShadowWriteTimeout time.Duration
	ShadowQueueSize    int
}

func (config *Config) normalize() error {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.UDPIdleTimeout == 0 {
		config.UDPIdleTimeout = 30 * time.Second
	}
	if config.PrimaryDialTimeout == 0 {
		config.PrimaryDialTimeout = 5 * time.Second
	}
	if config.ShadowWriteTimeout == 0 {
		config.ShadowWriteTimeout = 2 * time.Second
	}
	if config.ShadowQueueSize == 0 {
		config.ShadowQueueSize = 64
	}
	if config.UDPIdleTimeout < 100*time.Millisecond || config.PrimaryDialTimeout < 100*time.Millisecond ||
		config.ShadowWriteTimeout < 100*time.Millisecond || config.ShadowQueueSize < 1 || config.ShadowQueueSize > 1024 {
		return errors.New("Mirror relay configuration is invalid")
	}
	return nil
}

type Relay struct{ relay *mirrorRelay }

func New(
	connection *websocket.Conn,
	listeners *trafficlistener.Listeners,
	backends []trafficcontrol.BackendSet,
	config Config,
) (*Relay, error) {
	if connection == nil || listeners == nil {
		return nil, errors.New("Mirror connection and listeners are required")
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	primaries, err := newPrimaryPool(backends, config.PrimaryDialContext)
	if err != nil {
		return nil, err
	}
	return &Relay{relay: newMirrorRelay(connection, listeners, primaries, config)}, nil
}

func (relay *Relay) Ready(ctx context.Context) error {
	return relay.relay.writeFrame(ctx, mirrorstream.Frame{Type: mirrorstream.Ready})
}

func (relay *Relay) Run(ctx context.Context) error { return relay.relay.run(ctx) }

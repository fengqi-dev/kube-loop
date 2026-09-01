package mirror

import (
	"context"
	"errors"
	"net"
	"time"
)

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type Config struct {
	DialContext        DialContextFunc
	ShadowQueueSize    int
	ShadowDialTimeout  time.Duration
	ShadowWriteTimeout time.Duration
	ShadowIdleTimeout  time.Duration
	TrafficStreams     TrafficStreamOpener
}

const (
	defaultShadowQueueSize    = 16
	defaultShadowDialTimeout  = 2 * time.Second
	defaultShadowWriteTimeout = 500 * time.Millisecond
	defaultShadowIdleTimeout  = 30 * time.Second
)

func NewManager(client Client, config Config) (*Manager, error) {
	if client == nil || config.TrafficStreams == nil {
		return nil, errors.New("mirror control client and Data Plane stream opener are required")
	}
	streams := config.TrafficStreams
	config.TrafficStreams = nil
	if config.DialContext == nil {
		dialer := &net.Dialer{}
		config.DialContext = dialer.DialContext
	}
	if config.ShadowQueueSize == 0 {
		config.ShadowQueueSize = defaultShadowQueueSize
	}
	if config.ShadowDialTimeout == 0 {
		config.ShadowDialTimeout = defaultShadowDialTimeout
	}
	if config.ShadowWriteTimeout == 0 {
		config.ShadowWriteTimeout = defaultShadowWriteTimeout
	}
	if config.ShadowIdleTimeout == 0 {
		config.ShadowIdleTimeout = defaultShadowIdleTimeout
	}
	if config.ShadowQueueSize < 1 || config.ShadowQueueSize > 1024 ||
		config.ShadowDialTimeout < 100*time.Millisecond || config.ShadowDialTimeout > time.Minute ||
		config.ShadowWriteTimeout < 10*time.Millisecond || config.ShadowWriteTimeout > time.Minute ||
		config.ShadowIdleTimeout < 100*time.Millisecond || config.ShadowIdleTimeout > 24*time.Hour {
		return nil, errors.New("mirror shadow queue or timeout configuration is invalid")
	}
	return &Manager{
		client: client, streams: streams, dial: config.DialContext,
		config: config, active: make(map[string]*activeMirror), deleted: make(map[string]struct{}),
	}, nil
}

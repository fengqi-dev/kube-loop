package mirrorapi

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultCredentialCheckInterval = 5 * time.Second
	defaultTaskCheckInterval       = time.Second
	defaultUDPIdleTimeout          = 30 * time.Second
	defaultRestoreTimeout          = 30 * time.Second
	defaultPrimaryDialTimeout      = 5 * time.Second
	defaultShadowWriteTimeout      = 2 * time.Second
	defaultShadowQueueSize         = 64
)

type Config struct {
	GatewayIP               string
	OwnerID                 string
	Now                     func() time.Time
	CredentialCheckInterval time.Duration
	TaskCheckInterval       time.Duration
	UDPIdleTimeout          time.Duration
	RestoreTimeout          time.Duration
	PrimaryDialTimeout      time.Duration
	PrimaryDialContext      func(context.Context, string, string) (net.Conn, error)
	ShadowWriteTimeout      time.Duration
	ShadowQueueSize         int
}

func (config *Config) normalize() error {
	config.GatewayIP = strings.TrimSpace(config.GatewayIP)
	address := net.ParseIP(config.GatewayIP)
	if address == nil || address.IsUnspecified() {
		return errors.New("Mirror Gateway IP must be a concrete IP address")
	}
	config.OwnerID = strings.TrimSpace(config.OwnerID)
	if config.OwnerID == "" {
		config.OwnerID = uuid.NewString()
	}
	if len(config.OwnerID) > 253 {
		return errors.New("Mirror owner ID is too long")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.CredentialCheckInterval == 0 {
		config.CredentialCheckInterval = defaultCredentialCheckInterval
	}
	if config.TaskCheckInterval == 0 {
		config.TaskCheckInterval = defaultTaskCheckInterval
	}
	if config.UDPIdleTimeout == 0 {
		config.UDPIdleTimeout = defaultUDPIdleTimeout
	}
	if config.RestoreTimeout == 0 {
		config.RestoreTimeout = defaultRestoreTimeout
	}
	if config.PrimaryDialTimeout == 0 {
		config.PrimaryDialTimeout = defaultPrimaryDialTimeout
	}
	if config.ShadowWriteTimeout == 0 {
		config.ShadowWriteTimeout = defaultShadowWriteTimeout
	}
	if config.ShadowQueueSize == 0 {
		config.ShadowQueueSize = defaultShadowQueueSize
	}
	if config.CredentialCheckInterval < 10*time.Millisecond || config.CredentialCheckInterval > 30*time.Second {
		return errors.New("Mirror credential check interval must be between 10ms and 30s")
	}
	if config.TaskCheckInterval < 10*time.Millisecond || config.TaskCheckInterval > 30*time.Second {
		return errors.New("Mirror Task check interval must be between 10ms and 30s")
	}
	if config.UDPIdleTimeout < 100*time.Millisecond || config.UDPIdleTimeout > 24*time.Hour {
		return errors.New("Mirror UDP idle timeout must be between 100ms and 24h")
	}
	if config.RestoreTimeout < time.Second || config.RestoreTimeout > 5*time.Minute {
		return errors.New("Mirror restore timeout must be between 1s and 5m")
	}
	if config.PrimaryDialTimeout < 100*time.Millisecond || config.PrimaryDialTimeout > time.Minute {
		return errors.New("Mirror primary dial timeout must be between 100ms and 1m")
	}
	if config.ShadowWriteTimeout < 100*time.Millisecond || config.ShadowWriteTimeout > time.Minute {
		return errors.New("Mirror shadow write timeout must be between 100ms and 1m")
	}
	if config.ShadowQueueSize < 1 || config.ShadowQueueSize > 1024 {
		return errors.New("Mirror shadow queue size must be between 1 and 1024")
	}
	return nil
}

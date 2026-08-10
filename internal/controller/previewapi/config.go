package previewapi

import (
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
	defaultDeleteTimeout           = 30 * time.Second
)

type Config struct {
	GatewayIP               string
	OwnerID                 string
	Now                     func() time.Time
	CredentialCheckInterval time.Duration
	TaskCheckInterval       time.Duration
	UDPIdleTimeout          time.Duration
	DeleteTimeout           time.Duration
}

func (config *Config) normalize() error {
	config.GatewayIP = strings.TrimSpace(config.GatewayIP)
	address := net.ParseIP(config.GatewayIP)
	if address == nil || address.IsUnspecified() {
		return errors.New("Preview Gateway IP must be a concrete IP address")
	}
	config.OwnerID = strings.TrimSpace(config.OwnerID)
	if config.OwnerID == "" {
		config.OwnerID = uuid.NewString()
	}
	if len(config.OwnerID) > 253 {
		return errors.New("Preview owner ID is too long")
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
	if config.DeleteTimeout == 0 {
		config.DeleteTimeout = defaultDeleteTimeout
	}
	if config.CredentialCheckInterval < 10*time.Millisecond || config.CredentialCheckInterval > 30*time.Second {
		return errors.New("Preview credential check interval must be between 10ms and 30s")
	}
	if config.TaskCheckInterval < 10*time.Millisecond || config.TaskCheckInterval > 30*time.Second {
		return errors.New("Preview Task check interval must be between 10ms and 30s")
	}
	if config.UDPIdleTimeout < 100*time.Millisecond || config.UDPIdleTimeout > 24*time.Hour {
		return errors.New("Preview UDP idle timeout must be between 100ms and 24h")
	}
	if config.DeleteTimeout < time.Second || config.DeleteTimeout > 5*time.Minute {
		return errors.New("Preview delete timeout must be between 1s and 5m")
	}
	return nil
}

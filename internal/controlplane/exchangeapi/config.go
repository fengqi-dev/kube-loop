package exchangeapi

import (
	"errors"
	"time"
)

const defaultRestoreTimeout = 30 * time.Second

type Config struct {
	Now            func() time.Time
	RestoreTimeout time.Duration
}

func (config *Config) normalize() error {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RestoreTimeout == 0 {
		config.RestoreTimeout = defaultRestoreTimeout
	}
	if config.RestoreTimeout < time.Second ||
		config.RestoreTimeout > 5*time.Minute {
		return errors.New("exchange restore timeout must be between 1s and 5m")
	}
	return nil
}

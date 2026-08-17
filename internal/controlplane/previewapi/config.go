package previewapi

import (
	"errors"
	"time"
)

const defaultDeleteTimeout = 30 * time.Second

type Config struct {
	Now           func() time.Time
	DeleteTimeout time.Duration
}

func (config *Config) normalize() error {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DeleteTimeout == 0 {
		config.DeleteTimeout = defaultDeleteTimeout
	}
	if config.DeleteTimeout < time.Second || config.DeleteTimeout > 5*time.Minute {
		return errors.New("Preview delete timeout must be between 1s and 5m")
	}
	return nil
}

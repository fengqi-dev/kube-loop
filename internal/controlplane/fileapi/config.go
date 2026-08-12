package fileapi

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
)

const (
	maximumBytesEnv = "KUBELOOP_FILE_MAX_BYTES"
	allowedRootsEnv = "KUBELOOP_FILE_ALLOWED_ROOTS_JSON"
)

func ConfigFromEnv() (Config, error) {
	config := Config{}
	if raw := strings.TrimSpace(os.Getenv(maximumBytesEnv)); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Config{}, errors.New("KUBELOOP_FILE_MAX_BYTES must be an unsigned integer")
		}
		config.MaximumBytes = value
	}
	if raw := strings.TrimSpace(os.Getenv(allowedRootsEnv)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &config.AllowedPathRoots); err != nil || len(config.AllowedPathRoots) == 0 {
			return Config{}, errors.New("KUBELOOP_FILE_ALLOWED_ROOTS_JSON must be a non-empty JSON string array")
		}
	}
	return config, nil
}

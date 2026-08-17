package fileapi

import (
	"encoding/json"
	"errors"
	"strings"

	env "github.com/Netflix/go-env"
)

const (
	maximumBytesEnv = "KUBELOOP_FILE_MAX_BYTES"
	allowedRootsEnv = "KUBELOOP_FILE_ALLOWED_ROOTS_JSON"
)

type fileEnvironment struct {
	MaximumBytes uint64          `env:"KUBELOOP_FILE_MAX_BYTES"`
	AllowedRoots jsonStringSlice `env:"KUBELOOP_FILE_ALLOWED_ROOTS_JSON"`
}

type jsonStringSlice []string

func (value *jsonStringSlice) UnmarshalEnvironmentValue(raw string) error {
	var decoded []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &decoded); err != nil || len(decoded) == 0 {
		return errors.New("KUBELOOP_FILE_ALLOWED_ROOTS_JSON must be a non-empty JSON string array")
	}
	*value = decoded
	return nil
}

func ConfigFromEnv() (Config, error) {
	var environment fileEnvironment
	if _, err := env.UnmarshalFromEnviron(&environment); err != nil {
		return Config{}, err
	}
	return Config{MaximumBytes: environment.MaximumBytes, AllowedPathRoots: []string(environment.AllowedRoots)}, nil
}

package kubernetes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	DefaultTimeout   = 15 * time.Second
	DefaultQPS       = float32(20)
	DefaultBurst     = 40
	DefaultUserAgent = "kube-loop-controller/dev"
	maxConfigBytes   = 1 << 20
)

type Config struct {
	Timeout       time.Duration
	QPS           float32
	Burst         int
	UserAgent     string
	Impersonation ImpersonationConfig
}

type ImpersonationConfig struct {
	Enabled        bool
	UsernamePrefix string
	GroupMappings  map[string][]string
}

type configFile struct {
	Timeout       string                  `json:"timeout,omitempty"`
	QPS           float32                 `json:"qps,omitempty"`
	Burst         int                     `json:"burst,omitempty"`
	UserAgent     string                  `json:"userAgent,omitempty"`
	Impersonation impersonationConfigFile `json:"impersonation"`
}

type impersonationConfigFile struct {
	Enabled        bool                `json:"enabled,omitempty"`
	UsernamePrefix string              `json:"usernamePrefix,omitempty"`
	GroupMappings  map[string][]string `json:"groupMappings,omitempty"`
}

func Load(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}.normalized()
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open Kubernetes configuration: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read Kubernetes configuration: %w", err)
	}
	if len(contents) > maxConfigBytes {
		return Config{}, errors.New("Kubernetes configuration exceeds 1 MiB")
	}
	var document configFile
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Config{}, fmt.Errorf("decode Kubernetes configuration: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Config{}, err
	}
	config := Config{
		QPS: document.QPS, Burst: document.Burst, UserAgent: document.UserAgent,
		Impersonation: ImpersonationConfig{
			Enabled:        document.Impersonation.Enabled,
			UsernamePrefix: document.Impersonation.UsernamePrefix,
			GroupMappings:  document.Impersonation.GroupMappings,
		},
	}
	if strings.TrimSpace(document.Timeout) != "" {
		config.Timeout, err = time.ParseDuration(document.Timeout)
		if err != nil {
			return Config{}, fmt.Errorf("parse Kubernetes timeout: %w", err)
		}
	}
	return config.normalized()
}

func (config Config) normalized() (Config, error) {
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	if config.Timeout < time.Second || config.Timeout > 5*time.Minute {
		return Config{}, errors.New("Kubernetes timeout must be between 1 second and 5 minutes")
	}
	if config.QPS == 0 {
		config.QPS = DefaultQPS
	}
	if config.QPS < 0.1 || config.QPS > 1000 {
		return Config{}, errors.New("Kubernetes QPS must be between 0.1 and 1000")
	}
	if config.Burst == 0 {
		config.Burst = DefaultBurst
	}
	if config.Burst < 1 || config.Burst > 10000 {
		return Config{}, errors.New("Kubernetes burst must be between 1 and 10000")
	}
	config.UserAgent = strings.TrimSpace(config.UserAgent)
	if config.UserAgent == "" {
		config.UserAgent = DefaultUserAgent
	}
	if !safeValue(config.UserAgent, 256) {
		return Config{}, errors.New("Kubernetes user agent is invalid")
	}
	impersonation := config.Impersonation
	impersonation.UsernamePrefix = strings.TrimSpace(impersonation.UsernamePrefix)
	if impersonation.Enabled {
		if impersonation.UsernamePrefix == "" {
			impersonation.UsernamePrefix = "kubeloop:"
		}
		if !safeValue(impersonation.UsernamePrefix, 128) || strings.HasPrefix(strings.ToLower(impersonation.UsernamePrefix), "system:") {
			return Config{}, errors.New("Kubernetes impersonation username prefix is invalid")
		}
	}
	impersonation.GroupMappings = cloneGroupMappings(impersonation.GroupMappings)
	for identityGroup, kubernetesGroups := range impersonation.GroupMappings {
		if !safeValue(identityGroup, 256) {
			return Config{}, errors.New("Kubernetes impersonation contains an invalid identity group")
		}
		if len(kubernetesGroups) == 0 {
			return Config{}, fmt.Errorf("Kubernetes impersonation group %q has no mapped groups", identityGroup)
		}
		for _, group := range kubernetesGroups {
			if !safeValue(strings.TrimSpace(group), 256) {
				return Config{}, fmt.Errorf("Kubernetes impersonation group %q has an invalid mapping", identityGroup)
			}
		}
	}
	config.Impersonation = impersonation
	return config, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("Kubernetes configuration contains trailing JSON")
		}
		return fmt.Errorf("decode Kubernetes configuration: %w", err)
	}
	return nil
}

func safeValue(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func cloneGroupMappings(source map[string][]string) map[string][]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string][]string, len(source))
	for key, values := range source {
		normalizedValues := make([]string, 0, len(values))
		for _, value := range values {
			normalizedValues = append(normalizedValues, strings.TrimSpace(value))
		}
		result[key] = normalizedValues
	}
	return result
}

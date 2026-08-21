package controlplane

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultListenAddress       = ":8080"
	DefaultServiceID           = "kubeloop"
	DefaultTunnelPath          = "/tunnel"
	DefaultShutdownTimeout     = 10 * time.Second
	DefaultAPIRequestTimeout   = 30 * time.Second
	DefaultMaxRequestBodyBytes = int64(1 << 20)
	DefaultMaxHeaderBytes      = 64 << 10
	DefaultReadinessTimeout    = 2 * time.Second
)

type Config struct {
	ListenAddress       string
	PublicURL           string
	ServiceID           string
	TunnelPath          string
	MinClientVersion    string
	AuthMethods         []AuthMethod
	ShutdownTimeout     time.Duration
	APIRequestTimeout   time.Duration
	MaxRequestBodyBytes int64
	ReadinessTimeout    time.Duration
}

func (config Config) normalized() (Config, error) {
	config.ListenAddress = strings.TrimSpace(config.ListenAddress)
	if config.ListenAddress == "" {
		config.ListenAddress = DefaultListenAddress
	}
	var err error
	if config.ServiceID, err = normalizeServiceID(config.ServiceID); err != nil {
		return Config{}, err
	}
	if config.PublicURL, err = normalizePublicURL(config.PublicURL); err != nil {
		return Config{}, err
	}
	config.MinClientVersion = strings.TrimSpace(config.MinClientVersion)
	if config.TunnelPath, err = normalizeTunnelPath(config.TunnelPath); err != nil {
		return Config{}, err
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = DefaultShutdownTimeout
	}
	if config.APIRequestTimeout <= 0 {
		config.APIRequestTimeout = DefaultAPIRequestTimeout
	}
	if config.MaxRequestBodyBytes <= 0 {
		config.MaxRequestBodyBytes = DefaultMaxRequestBodyBytes
	}
	if config.ReadinessTimeout <= 0 {
		config.ReadinessTimeout = DefaultReadinessTimeout
	}
	if err := normalizeAuthMethods(config.AuthMethods); err != nil {
		return Config{}, err
	}
	return config, nil
}

func normalizeServiceID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = DefaultServiceID
	}
	if len(value) > 128 {
		return "", errors.New("service ID must not exceed 128 characters")
	}
	for _, character := range value {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		separator := character == '-' || character == '_' || character == '.'
		if !letter && !digit && !separator {
			return "", errors.New("service ID may only contain letters, numbers, '.', '_' and '-'")
		}
	}
	return value, nil
}

func normalizePublicURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", errors.New("public URL is required")
	}
	publicURL, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse public URL: %w", err)
	}
	if !publicURL.IsAbs() || (publicURL.Scheme != "https" && publicURL.Scheme != "http") ||
		publicURL.Host == "" {
		return "", errors.New("public URL must be an absolute HTTP or HTTPS URL")
	}
	if publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" {
		return "", errors.New("public URL must not contain user info, query parameters or a fragment")
	}
	if publicURL.Path != "" || publicURL.RawPath != "" {
		return "", errors.New("public URL must be an origin without a path")
	}
	return publicURL.String(), nil
}

func normalizeTunnelPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = DefaultTunnelPath
	}
	tunnelURL, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(value, "/") || tunnelURL.IsAbs() || tunnelURL.Host != "" ||
		tunnelURL.RawQuery != "" || tunnelURL.Fragment != "" || tunnelURL.EscapedPath() != value ||
		strings.Contains(value, "//") || strings.Contains(value, "/./") || strings.Contains(value, "/../") ||
		strings.HasSuffix(value, "/.") || strings.HasSuffix(value, "/..") {
		return "", errors.New(
			"tunnel path must be a clean absolute URL path without escaping, query parameters or a fragment",
		)
	}
	return value, nil
}

func normalizeAuthMethods(methods []AuthMethod) error {
	providerIDs := make(map[string]struct{}, len(methods))
	for index := range methods {
		methods[index].ID = strings.TrimSpace(methods[index].ID)
		methods[index].Type = strings.ToLower(strings.TrimSpace(methods[index].Type))
		methods[index].DisplayName = strings.TrimSpace(methods[index].DisplayName)
		methods[index].Interaction = strings.ToLower(strings.TrimSpace(methods[index].Interaction))
		if methods[index].ID == "" || methods[index].Type == "" {
			return fmt.Errorf("auth method %d requires both ID and type", index)
		}
		if _, exists := providerIDs[methods[index].ID]; exists {
			return fmt.Errorf("auth method %d has duplicate ID %q", index, methods[index].ID)
		}
		providerIDs[methods[index].ID] = struct{}{}
		if methods[index].Type != providerOIDC && methods[index].Type != providerLocal {
			return fmt.Errorf("auth method %d has unsupported type %q", index, methods[index].Type)
		}
		if methods[index].Interaction == "" {
			methods[index].Interaction = interactionBrowser
		}
		if methods[index].Interaction != interactionBrowser {
			return fmt.Errorf("auth method %d requires browser interaction", index)
		}
	}
	return nil
}

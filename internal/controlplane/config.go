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
	config.ServiceID = strings.TrimSpace(config.ServiceID)
	if config.ServiceID == "" {
		config.ServiceID = DefaultServiceID
	}
	if len(config.ServiceID) > 128 {
		return Config{}, errors.New("service ID must not exceed 128 characters")
	}
	for _, character := range config.ServiceID {
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			character != '-' && character != '_' && character != '.' {
			return Config{}, errors.New("service ID may only contain letters, numbers, '.', '_' and '-'")
		}
	}
	config.PublicURL = strings.TrimRight(strings.TrimSpace(config.PublicURL), "/")
	if config.PublicURL == "" {
		return Config{}, errors.New("public URL is required")
	}
	publicURL, err := url.Parse(config.PublicURL)
	if err != nil {
		return Config{}, fmt.Errorf("parse public URL: %w", err)
	}
	if !publicURL.IsAbs() || (publicURL.Scheme != "https" && publicURL.Scheme != "http") || publicURL.Host == "" {
		return Config{}, errors.New("public URL must be an absolute HTTP or HTTPS URL")
	}
	if publicURL.Scheme == "http" && publicURL.Hostname() != "localhost" && publicURL.Hostname() != "127.0.0.1" && publicURL.Hostname() != "::1" {
		return Config{}, errors.New("public URL must use HTTPS except for loopback development addresses")
	}
	if publicURL.User != nil || publicURL.RawQuery != "" || publicURL.Fragment != "" {
		return Config{}, errors.New("public URL must not contain user info, query parameters or a fragment")
	}
	if publicURL.Path != "" || publicURL.RawPath != "" {
		return Config{}, errors.New("public URL must be an origin without a path")
	}
	config.PublicURL = publicURL.String()
	config.MinClientVersion = strings.TrimSpace(config.MinClientVersion)
	config.TunnelPath = strings.TrimSpace(config.TunnelPath)
	if config.TunnelPath == "" {
		config.TunnelPath = DefaultTunnelPath
	}
	tunnelURL, err := url.ParseRequestURI(config.TunnelPath)
	if err != nil || !strings.HasPrefix(config.TunnelPath, "/") || tunnelURL.IsAbs() || tunnelURL.Host != "" ||
		tunnelURL.RawQuery != "" || tunnelURL.Fragment != "" || tunnelURL.EscapedPath() != config.TunnelPath ||
		strings.Contains(config.TunnelPath, "//") || strings.Contains(config.TunnelPath, "/./") ||
		strings.Contains(config.TunnelPath, "/../") || strings.HasSuffix(config.TunnelPath, "/.") ||
		strings.HasSuffix(config.TunnelPath, "/..") {
		return Config{}, errors.New("tunnel path must be a clean absolute URL path without escaping, query parameters or a fragment")
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
	providerIDs := make(map[string]struct{}, len(config.AuthMethods))
	for index := range config.AuthMethods {
		config.AuthMethods[index].ID = strings.TrimSpace(config.AuthMethods[index].ID)
		config.AuthMethods[index].Type = strings.ToLower(strings.TrimSpace(config.AuthMethods[index].Type))
		config.AuthMethods[index].DisplayName = strings.TrimSpace(config.AuthMethods[index].DisplayName)
		config.AuthMethods[index].Interaction = strings.ToLower(strings.TrimSpace(config.AuthMethods[index].Interaction))
		if config.AuthMethods[index].ID == "" || config.AuthMethods[index].Type == "" {
			return Config{}, fmt.Errorf("auth method %d requires both ID and type", index)
		}
		if _, exists := providerIDs[config.AuthMethods[index].ID]; exists {
			return Config{}, fmt.Errorf("auth method %d has duplicate ID %q", index, config.AuthMethods[index].ID)
		}
		providerIDs[config.AuthMethods[index].ID] = struct{}{}
		switch config.AuthMethods[index].Type {
		case "oidc", "local":
			if config.AuthMethods[index].Interaction == "" {
				config.AuthMethods[index].Interaction = "browser"
			}
			if config.AuthMethods[index].Interaction != "browser" {
				return Config{}, fmt.Errorf("auth method %d requires browser interaction", index)
			}
		case "anonymous":
			if config.AuthMethods[index].Interaction != "none" {
				return Config{}, fmt.Errorf("auth method %d requires none interaction", index)
			}
		default:
			return Config{}, fmt.Errorf("auth method %d has unsupported type %q", index, config.AuthMethods[index].Type)
		}
	}
	return config, nil
}

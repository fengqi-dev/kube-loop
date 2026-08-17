package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"sigs.k8s.io/yaml"
)

const (
	gatewayConfigFileEnvironment = "KUBELOOP_GATEWAY_CONFIG_FILE"
	maximumGatewayConfigBytes    = 4 << 20
)

type gatewayConfig struct {
	HTTP             gatewayHTTPConfig      `json:"http"`
	Relay            gatewayRelayConfig     `json:"relay"`
	WebSocket        gatewayWebSocketConfig `json:"websocket"`
	MinClientVersion string                 `json:"minClientVersion,omitempty"`
	DrainTimeout     jsonDuration           `json:"drainTimeout"`
	LogLevel         string                 `json:"logLevel"`
}

type gatewayHTTPConfig struct {
	Listen string `json:"listen"`
	Path   string `json:"path"`
}

type gatewayRelayConfig struct {
	ControlPlaneURL       string `json:"controlPlaneURL"`
	Endpoint              string `json:"endpoint"`
	ClientCertificateFile string `json:"clientCertificateFile,omitempty"`
	ClientPrivateKeyFile  string `json:"clientPrivateKeyFile,omitempty"`
	ServerCAFile          string `json:"serverCAFile,omitempty"`
	ServerName            string `json:"serverName,omitempty"`
	BearerTokenFile       string `json:"bearerTokenFile,omitempty"`
	ReplayEntries         int    `json:"replayEntries"`
}

type gatewayWebSocketConfig struct {
	MaxSessions          int          `json:"maxSessions"`
	MaxSessionsPerUser   int          `json:"maxSessionsPerUser"`
	MaxStreamsPerSession int          `json:"maxStreamsPerSession"`
	MaxFrameBytes        int64        `json:"maxFrameBytes"`
	HandshakeTimeout     jsonDuration `json:"handshakeTimeout"`
	StreamIdleTimeout    jsonDuration `json:"streamIdleTimeout"`
}

type jsonDuration struct{ time.Duration }

func (duration *jsonDuration) UnmarshalJSON(raw []byte) error {
	return duration.unmarshal(raw)
}

func (duration *jsonDuration) unmarshal(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("duration must be a string")
	}
	return duration.parse(value)
}

func (duration *jsonDuration) parse(value string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fmt.Errorf("duration %q must be positive", value)
	}
	duration.Duration = parsed
	return nil
}

func defaultGatewayConfig() gatewayConfig {
	return gatewayConfig{
		HTTP:  gatewayHTTPConfig{Listen: ":8080", Path: websocketmux.DefaultPath},
		Relay: gatewayRelayConfig{ReplayEntries: relayticket.DefaultReplayEntries},
		WebSocket: gatewayWebSocketConfig{
			MaxSessions: 256, MaxSessionsPerUser: 8, MaxStreamsPerSession: 128, MaxFrameBytes: 1 << 20,
			HandshakeTimeout:  jsonDuration{Duration: 10 * time.Second},
			StreamIdleTimeout: jsonDuration{Duration: 30 * time.Minute},
		},
		DrainTimeout: jsonDuration{Duration: 30 * time.Second},
		LogLevel:     "info",
	}
}

func loadGatewayConfig(path string) (gatewayConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return gatewayConfig{}, fmt.Errorf("%s is required", gatewayConfigFileEnvironment)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return gatewayConfig{}, fmt.Errorf("read Gateway configuration: %w", err)
	}
	if len(raw) == 0 || len(raw) > maximumGatewayConfigBytes {
		return gatewayConfig{}, errors.New("Gateway configuration size is invalid")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(raw, &fields); err != nil {
		return gatewayConfig{}, fmt.Errorf("decode Gateway configuration: %w", err)
	}
	if fields["controlPlane"] == nil || fields["gateway"] == nil {
		return gatewayConfig{}, errors.New("unified configuration requires controlPlane and gateway")
	}
	root := struct {
		ControlPlane any           `json:"controlPlane"`
		Gateway      gatewayConfig `json:"gateway"`
	}{Gateway: defaultGatewayConfig()}
	if err := yaml.UnmarshalStrict(raw, &root); err != nil {
		return gatewayConfig{}, fmt.Errorf("decode Gateway configuration: %w", err)
	}
	config := root.Gateway
	if err := config.normalizeAndValidate(); err != nil {
		return gatewayConfig{}, err
	}
	return config, nil
}

func (config *gatewayConfig) normalizeAndValidate() error {
	config.HTTP.Listen = strings.TrimSpace(config.HTTP.Listen)
	config.HTTP.Path = strings.TrimSpace(config.HTTP.Path)
	config.Relay.ControlPlaneURL = strings.TrimSpace(config.Relay.ControlPlaneURL)
	config.Relay.Endpoint = strings.TrimSpace(config.Relay.Endpoint)
	config.Relay.ClientCertificateFile = strings.TrimSpace(config.Relay.ClientCertificateFile)
	config.Relay.ClientPrivateKeyFile = strings.TrimSpace(config.Relay.ClientPrivateKeyFile)
	config.Relay.ServerCAFile = strings.TrimSpace(config.Relay.ServerCAFile)
	config.Relay.ServerName = strings.TrimSpace(config.Relay.ServerName)
	config.Relay.BearerTokenFile = strings.TrimSpace(config.Relay.BearerTokenFile)
	config.MinClientVersion = strings.TrimSpace(config.MinClientVersion)
	config.LogLevel = strings.ToLower(strings.TrimSpace(config.LogLevel))

	if config.HTTP.Listen == "" || !strings.HasPrefix(config.HTTP.Path, "/") {
		return errors.New("Gateway HTTP listen address and absolute path are required")
	}
	if config.Relay.ControlPlaneURL == "" || config.Relay.Endpoint == "" || config.Relay.ReplayEntries < 1 {
		return errors.New("Gateway Relay configuration is invalid")
	}
	websocket := config.WebSocket
	if websocket.MaxSessions < 1 || websocket.MaxSessions > 1<<20 ||
		websocket.MaxSessionsPerUser < 1 || websocket.MaxSessionsPerUser > websocket.MaxSessions ||
		websocket.MaxStreamsPerSession < 1 || websocket.MaxStreamsPerSession > 1<<20 ||
		websocket.MaxFrameBytes < 8<<10 || websocket.MaxFrameBytes > 16<<20 ||
		uint64(websocket.MaxSessions)*uint64(websocket.MaxStreamsPerSession) > 1<<24 ||
		websocket.HandshakeTimeout.Duration <= 0 || websocket.StreamIdleTimeout.Duration <= 0 || config.DrainTimeout.Duration <= 0 {
		return errors.New("Gateway WebSocket capacity or timeout configuration is invalid")
	}
	switch config.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("Gateway log level must be debug, info, warn, or error")
	}
	return nil
}

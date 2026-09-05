package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/fengqi-dev/kube-loop/internal/auth/relaybearer"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trojanws"
)

const (
	ConfigFileEnvironment     = "KUBELOOP_GATEWAY_CONFIG_FILE"
	maximumGatewayConfigBytes = 4 << 20
)

type Config struct {
	HTTP             HTTPConfig      `json:"http"`
	Relay            RelayConfig     `json:"relay"`
	WebSocket        WebSocketConfig `json:"websocket"`
	Forward          ForwardConfig   `json:"forward"`
	MinClientVersion string          `json:"minClientVersion,omitempty"`
	DrainTimeout     Duration        `json:"drainTimeout"`
	LogLevel         string          `json:"logLevel"`
}

type ForwardConfig struct {
	Enabled     bool   `json:"enabled"`
	Path        string `json:"path"`
	SingBoxPath string `json:"singBoxPath"`
}

type HTTPConfig struct {
	Listen string `json:"listen"`
	Path   string `json:"path"`
}

type RelayConfig struct {
	ControlPlaneURL       string `json:"controlPlaneURL"`
	Endpoint              string `json:"endpoint"`
	ClientCertificateFile string `json:"clientCertificateFile,omitempty"`
	ClientPrivateKeyFile  string `json:"clientPrivateKeyFile,omitempty"`
	ServerCAFile          string `json:"serverCAFile,omitempty"`
	ServerName            string `json:"serverName,omitempty"`
	BearerTokenFile       string `json:"bearerTokenFile,omitempty"`
	ReplayEntries         int    `json:"replayEntries"`
}

type WebSocketConfig struct {
	MaxSessions          int      `json:"maxSessions"`
	MaxSessionsPerUser   int      `json:"maxSessionsPerUser"`
	MaxStreamsPerSession int      `json:"maxStreamsPerSession"`
	MaxFrameBytes        int64    `json:"maxFrameBytes"`
	HandshakeTimeout     Duration `json:"handshakeTimeout"`
	StreamIdleTimeout    Duration `json:"streamIdleTimeout"`
	TrafficEncryption    *bool    `json:"trafficEncryption"`
}

type Duration struct{ time.Duration }

func (duration *Duration) UnmarshalJSON(raw []byte) error {
	return duration.unmarshal(raw)
}

func (duration *Duration) unmarshal(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("duration must be a string")
	}
	return duration.parse(value)
}

func (duration *Duration) parse(value string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fmt.Errorf("duration %q must be positive", value)
	}
	duration.Duration = parsed
	return nil
}

func defaultConfig() Config {
	return Config{
		HTTP:  HTTPConfig{Listen: ":8080", Path: websocketmux.DefaultPath},
		Relay: RelayConfig{ReplayEntries: relaybearer.DefaultReplayEntries},
		WebSocket: WebSocketConfig{
			MaxSessions: 256, MaxSessionsPerUser: 8, MaxStreamsPerSession: 128, MaxFrameBytes: 1 << 20,
			HandshakeTimeout:  Duration{Duration: 10 * time.Second},
			StreamIdleTimeout: Duration{Duration: 30 * time.Minute},
			TrafficEncryption: new(true),
		},
		Forward:      ForwardConfig{Enabled: true, Path: trojanws.DefaultPath, SingBoxPath: "/sing-box"},
		DrainTimeout: Duration{Duration: 30 * time.Second},
		LogLevel:     "info",
	}
}

func LoadConfig(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, fmt.Errorf("%s is required", ConfigFileEnvironment)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read Gateway configuration: %w", err)
	}
	if len(raw) == 0 || len(raw) > maximumGatewayConfigBytes {
		return Config{}, errors.New("gateway configuration size is invalid")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(raw, &fields); err != nil {
		return Config{}, fmt.Errorf("decode Gateway configuration: %w", err)
	}
	if fields["controlPlane"] == nil || fields["gateway"] == nil {
		return Config{}, errors.New("unified configuration requires controlPlane and gateway")
	}
	root := struct {
		ControlPlane any    `json:"controlPlane"`
		Gateway      Config `json:"gateway"`
	}{Gateway: defaultConfig()}
	if err := yaml.UnmarshalStrict(raw, &root); err != nil {
		return Config{}, fmt.Errorf("decode Gateway configuration: %w", err)
	}
	config := root.Gateway
	if err := config.normalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) normalizeAndValidate() error {
	config.HTTP.Listen = strings.TrimSpace(config.HTTP.Listen)
	config.HTTP.Path = strings.TrimSpace(config.HTTP.Path)
	config.Forward.Path = strings.TrimSpace(config.Forward.Path)
	config.Forward.SingBoxPath = strings.TrimSpace(config.Forward.SingBoxPath)
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
		return errors.New("gateway HTTP listen address and absolute path are required")
	}
	if config.Forward.Enabled &&
		(config.Forward.Path == "" || !strings.HasPrefix(config.Forward.Path, "/") ||
			config.Forward.SingBoxPath == "") {
		return errors.New("gateway forward configuration is invalid")
	}
	if config.Relay.ControlPlaneURL == "" || config.Relay.Endpoint == "" || config.Relay.ReplayEntries < 1 {
		return errors.New("gateway Relay configuration is invalid")
	}
	websocket := config.WebSocket
	if websocket.MaxSessions < 1 || websocket.MaxSessions > 1<<20 ||
		websocket.MaxSessionsPerUser < 1 || websocket.MaxSessionsPerUser > websocket.MaxSessions ||
		websocket.MaxStreamsPerSession < 1 || websocket.MaxStreamsPerSession > 1<<20 ||
		websocket.MaxFrameBytes < 8<<10 || websocket.MaxFrameBytes > 16<<20 ||
		uint64(websocket.MaxSessions)*uint64(websocket.MaxStreamsPerSession) > 1<<24 ||
		websocket.HandshakeTimeout.Duration <= 0 ||
		websocket.StreamIdleTimeout.Duration <= 0 ||
		config.DrainTimeout.Duration <= 0 {
		return errors.New("gateway WebSocket capacity or timeout configuration is invalid")
	}
	switch config.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("gateway log level must be debug, info, warn, or error")
	}
	return nil
}

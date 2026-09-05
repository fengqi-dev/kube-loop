package singbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const (
	ClientTrojanSOCKSInbound = "kubeloop-socks-in"
	clientTrojanOutbound     = "kubeloop-trojan-out"
	webSocketSecureScheme    = "wss"
)

// ClientTrojanOptions describes the unprivileged local sing-box process that
// exposes SOCKS and carries forward traffic over Trojan/WebSocket.
type ClientTrojanOptions struct {
	SessionID      string
	ListenPort     int
	Endpoint       string
	RelayTicket    string
	TrojanPassword string
	TLSInsecure    bool
	LogLevel       string
}

func GenerateClientTrojanConfig(options ClientTrojanOptions) ([]byte, error) {
	if err := ValidateSessionID(options.SessionID); err != nil {
		return nil, fmt.Errorf("invalid Client Session ID: %w", err)
	}
	if err := validatePort(options.ListenPort, "Client SOCKS"); err != nil {
		return nil, err
	}
	if len(options.RelayTicket) == 0 || len(options.RelayTicket) > relayticket.MaximumTicketBytes ||
		strings.TrimSpace(options.RelayTicket) != options.RelayTicket || utils.ContainsControl(options.RelayTicket) {
		return nil, errors.New("invalid Client RelayTicket")
	}
	if err := validateTrojanPassword(options.TrojanPassword); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(strings.TrimSpace(options.Endpoint))
	validScheme := endpoint != nil &&
		(endpoint.Scheme == "ws" || endpoint.Scheme == webSocketSecureScheme)
	if err != nil || !validScheme || endpoint.Hostname() == "" {
		return nil, errors.New("invalid Client Trojan WebSocket endpoint")
	}
	port := 80
	if endpoint.Scheme == webSocketSecureScheme {
		port = 443
	}
	if endpoint.Port() != "" {
		port, err = strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid Client Trojan WebSocket port")
		}
	}
	if endpoint.Path == "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("invalid Client Trojan WebSocket path")
	}
	outbound := map[string]any{
		configTypeKey: "trojan", configTagKey: clientTrojanOutbound,
		"server": endpoint.Hostname(), "server_port": port,
		configPasswordKey: options.TrojanPassword,
		"multiplex": map[string]any{
			configEnabledKey: true, "protocol": "smux", "max_connections": 2,
		},
		"transport": map[string]any{
			"type": "ws", "path": endpoint.EscapedPath(),
			"headers": map[string]any{"Authorization": "Bearer " + options.RelayTicket},
		},
	}
	if endpoint.Scheme == webSocketSecureScheme {
		outbound["tls"] = map[string]any{
			configEnabledKey: true, "server_name": endpoint.Hostname(), "insecure": options.TLSInsecure,
		}
	}
	config := map[string]any{
		configLogKey: map[string]any{configLevelKey: normalizeLogLevel(options.LogLevel)},
		configInboundsKey: []map[string]any{{
			configTypeKey: configSOCKSType, configTagKey: ClientTrojanSOCKSInbound,
			configListenKey: DefaultDNSListen, configListenPortKey: options.ListenPort,
		}},
		configOutboundsKey: []map[string]any{outbound},
		configRouteKey:     map[string]any{configFinalKey: clientTrojanOutbound},
	}
	return json.MarshalIndent(config, "", "  ")
}

func validateTrojanPassword(password string) error {
	if len(password) != 64 || strings.TrimSpace(password) != password {
		return errors.New("invalid Trojan password")
	}
	for _, character := range password {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("invalid Trojan password")
		}
	}
	return nil
}

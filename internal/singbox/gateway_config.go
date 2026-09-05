package singbox

import (
	"encoding/json"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
)

const (
	GatewayTrojanInbound  = "kubeloop-trojan-in"
	gatewayDirectOutbound = "kubeloop-cluster"
	GatewayWebSocketPath  = "/_kubeloop/v3/session"
)

// GatewaySessionOptions describes one loopback sing-box runtime selected by an
// authenticated outer WebSocket. TLS and WebSocket termination intentionally
// remain in the KubeLoop adapter so RelayTicket authorization happens before
// any Trojan bytes are admitted.
type GatewaySessionOptions struct {
	SessionID      string
	ListenPort     int
	TrojanPassword string
	Network        NetworkSpec
	LogLevel       string
}

// GenerateGatewaySessionConfig renders a deny-by-default sing-box Trojan
// runtime for one authenticated Session generation.
func GenerateGatewaySessionConfig(options GatewaySessionOptions) ([]byte, error) {
	if err := ValidateSessionID(options.SessionID); err != nil {
		return nil, fmt.Errorf("invalid Gateway Session ID: %w", err)
	}
	if err := validatePort(options.ListenPort, "Gateway Trojan"); err != nil {
		return nil, err
	}
	if err := validateTrojanPassword(options.TrojanPassword); err != nil {
		return nil, fmt.Errorf("invalid Gateway credential: %w", err)
	}
	routes, err := clusterRoutes(options.Network)
	if err != nil {
		return nil, fmt.Errorf("build Gateway Session routes: %w", err)
	}
	domains, err := dns.NormalizeClusterDomains(options.Network.ClusterDomains)
	if err != nil {
		return nil, fmt.Errorf("build Gateway Session domains: %w", err)
	}

	allowRules := make([]map[string]any, 0, 2)
	allowRules = append(allowRules, map[string]any{
		configInboundKey:  []string{GatewayTrojanInbound},
		configIPCIDRKey:   routes,
		configOutboundKey: gatewayDirectOutbound,
	})
	if len(domains) > 0 {
		allowRules = append(allowRules, map[string]any{
			configInboundKey:      []string{GatewayTrojanInbound},
			configDomainSuffixKey: domains,
			configOutboundKey:     gatewayDirectOutbound,
		})
	}
	allowRules = append(allowRules, map[string]any{
		configInboundKey: []string{GatewayTrojanInbound},
		configActionKey:  rejectRouteAction,
	})

	config := map[string]any{
		configLogKey: map[string]any{configLevelKey: normalizeLogLevel(options.LogLevel)},
		configInboundsKey: []map[string]any{{
			configTypeKey:       "trojan",
			configTagKey:        GatewayTrojanInbound,
			configListenKey:     DefaultDNSListen,
			configListenPortKey: options.ListenPort,
			"users": []map[string]any{{
				"name": options.SessionID, configPasswordKey: options.TrojanPassword,
			}},
			"multiplex": map[string]any{configEnabledKey: true},
			"transport": map[string]any{
				"type": "ws", "path": GatewayWebSocketPath,
			},
		}},
		configOutboundsKey: []map[string]any{{
			configTypeKey: DirectOutbound,
			configTagKey:  gatewayDirectOutbound,
		}},
		configRouteKey: map[string]any{configRulesKey: allowRules},
	}
	return json.MarshalIndent(config, "", "  ")
}

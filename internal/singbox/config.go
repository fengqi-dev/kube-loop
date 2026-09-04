package singbox

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

const (
	KubernetesOutbound = "kubernetes"
	LocalOutbound      = "local"
	DirectOutbound     = "direct"
	DefaultDNSListen   = "127.0.0.1"
	DefaultDNSPort     = 1053
	defaultTUNAddress  = "198.19.0.1/30"
	defaultNamespace   = "default"

	configActionKey   = "action"
	configAuthUserKey = "auth_user"
	configIPCIDRKey   = "ip_cidr"
	configInboundKey  = "inbound"
	configModeKey     = "mode"
	configOutboundKey = "outbound"
	configRulesKey    = "rules"
	configServerKey   = "server"
	configTagKey      = "tag"
	configTypeKey     = "type"
	logicalRuleModeOr = "or"
	logicalRuleType   = "logical"
	hostsDNSServer    = "hosts"
	rejectRouteAction = "reject"

	// TrafficInbound is the single loopback SOCKS inbound for local feature
	// adapters. All local features share one auth_user (TrafficLocalUser);
	// routing identity is the SOCKS username.
	TrafficInbound = "traffic-in"

	// TrafficLocalUser is the sole SOCKS auth_user registered on traffic-in.
	TrafficLocalUser = "kube-loop"
)

// NetworkSpec is the data-plane view of a discovered cluster network.
// Session translates Kubernetes discovery into this type at the adapter boundary.
type NetworkSpec struct {
	PodCIDRs       []string
	PodIPs         []string
	ServiceCIDRs   []string
	ServiceIPs     []string
	DNSServer      string
	ClusterDomains []string
	Namespaces     []string
}

type Options struct {
	BridgeHost       string
	BridgePort       int
	ControllerHost   string
	ControllerPort   int
	ControllerSecret string
	DNSHost          string
	DNSPort          int
	TUNAddress       string
	Namespace        string
	ClusterDomains   []string
	Hosts            []sessionspec.HostAlias
	TrafficPorts     sessionspec.TrafficInboundPorts
	TrafficPassword  string
	LogLevel         string
}

func Generate(network NetworkSpec, options Options) ([]byte, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	routes, err := clusterRoutes(network)
	if err != nil {
		return nil, err
	}
	dnsConfig, err := buildDNSConfig(network, options)
	if err != nil {
		return nil, err
	}
	inbounds, err := buildInbounds(routes, options)
	if err != nil {
		return nil, err
	}
	routeRules := buildRouteRules(routes, dnsConfig.clusterDomains)

	config := map[string]any{
		"log": map[string]any{"level": normalizeLogLevel(options.LogLevel), "output": "sing-box.log"},
		"dns": map[string]any{
			"servers":  dnsConfig.servers,
			"rules":    dnsConfig.rules,
			"final":    LocalOutbound,
			"strategy": "prefer_ipv4",
		},
		"inbounds": inbounds,
		"outbounds": []map[string]any{
			{
				configTypeKey:   "socks",
				configTagKey:    KubernetesOutbound,
				configServerKey: options.BridgeHost,
				"server_port":   options.BridgePort,
				"version":       "5",
			},
			{configTypeKey: DirectOutbound, configTagKey: LocalOutbound},
			{configTypeKey: DirectOutbound, configTagKey: DirectOutbound},
		},
		"route": map[string]any{
			"rules":                   routeRules,
			"final":                   DirectOutbound,
			"auto_detect_interface":   true,
			"find_process":            true,
			"default_domain_resolver": LocalOutbound,
		},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": net.JoinHostPort(
					options.ControllerHost, strconv.Itoa(options.ControllerPort),
				),
				"secret": options.ControllerSecret,
			},
		},
	}

	return json.MarshalIndent(config, "", "  ")
}

// normalizeLogLevel maps a session log level onto a sing-box "level" value.
// The empty value (and any unknown value) resolves to the default "info".
func normalizeLogLevel(raw string) string {
	level, ok := validLogLevels[strings.ToLower(strings.TrimSpace(raw))]
	if !ok {
		return "info"
	}
	return level
}

// validLogLevels is the set of sing-box log levels accepted for a session.
var validLogLevels = map[string]string{
	"debug": "debug",
	"info":  "info",
	"warn":  "warn",
	"error": "error",
}

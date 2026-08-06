package singbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
)

const (
	KubernetesOutbound = "kubernetes"
	LocalOutbound      = "local"
	DirectOutbound     = "direct"
	DefaultDNSListen   = "127.0.0.1"
	DefaultDNSPort     = 1053
	defaultTUNAddress  = "198.19.0.1/30"

	// TrafficInbound is the single loopback SOCKS inbound for all feature adapters.
	// Feature identity is carried as SOCKS auth_user (see TrafficUser*).
	TrafficInbound = "traffic-in"

	TrafficUserPortForward  = "port-forward"
	TrafficUserExchange     = "exchange"
	TrafficUserPreview      = "preview"
	TrafficUserMirrorShadow = "mirror-shadow"
)

// TrafficInboundPorts holds the single fixed loopback SOCKS listen port used by
// all feature adapters. Targets remain dynamic in the SOCKS request; feature
// identity is the SOCKS username (TrafficUser*).
type TrafficInboundPorts struct {
	Listen int `json:"listen"`
}

// TrafficFeatureUsers returns every SOCKS auth user registered on traffic-in.
// Mirror primary reaches the original Pod via Gateway dial, not traffic-in.
func TrafficFeatureUsers() []string {
	return []string{
		TrafficUserPortForward,
		TrafficUserExchange,
		TrafficUserPreview,
		TrafficUserMirrorShadow,
	}
}

func clusterTrafficUsers() []string {
	return []string{TrafficUserPortForward}
}

func localTrafficUsers() []string {
	return []string{TrafficUserExchange, TrafficUserPreview, TrafficUserMirrorShadow}
}

// HostAlias maps a DNS name to an IPv4 address for the local dns-in resolver.
type HostAlias struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}

// NetworkSpec is the data-plane view of a discovered cluster network.
// Session translates Kubernetes discovery into this type at the adapter boundary.
type NetworkSpec struct {
	PodCIDRs       []string
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
	Hosts            []HostAlias
	TrafficPorts     TrafficInboundPorts
	TrafficPassword  string
}

func Generate(network NetworkSpec, options Options) ([]byte, error) {
	if options.BridgeHost == "" {
		options.BridgeHost = "127.0.0.1"
	}
	if options.ControllerHost == "" {
		options.ControllerHost = "127.0.0.1"
	}
	if options.DNSHost == "" {
		options.DNSHost = DefaultDNSListen
	}
	if options.DNSPort == 0 {
		options.DNSPort = DefaultDNSPort
	}
	if err := validatePort(options.BridgePort, "bridge"); err != nil {
		return nil, err
	}
	if err := validatePort(options.ControllerPort, "controller"); err != nil {
		return nil, err
	}
	if err := validatePort(options.DNSPort, "dns"); err != nil {
		return nil, err
	}
	if options.ControllerSecret == "" {
		return nil, errors.New("controller secret is required")
	}
	if options.TUNAddress == "" {
		options.TUNAddress = defaultTUNAddress
	}
	if _, err := netip.ParsePrefix(options.TUNAddress); err != nil {
		return nil, fmt.Errorf("invalid TUN address %q: %w", options.TUNAddress, err)
	}

	routes, err := clusterRoutes(network)
	if err != nil {
		return nil, err
	}

	hosts, err := NormalizeHostAliases(options.Hosts)
	if err != nil {
		return nil, err
	}

	clusterDomains, err := dnsname.NormalizeClusterDomains(options.ClusterDomains)
	if err != nil {
		return nil, err
	}
	if len(network.ClusterDomains) > 0 {
		merged, mergeErr := dnsname.NormalizeClusterDomains(append(append([]string{}, clusterDomains...), network.ClusterDomains...))
		if mergeErr != nil {
			return nil, mergeErr
		}
		clusterDomains = merged
	}
	dnsServers := make([]map[string]any, 0, 3)
	dnsRules := make([]map[string]any, 0, 4)
	if len(hosts) > 0 {
		predefined := make(map[string]any, len(hosts))
		domains := make([]string, 0, len(hosts))
		for _, item := range hosts {
			predefined[item.Domain] = item.IP
			domains = append(domains, item.Domain)
		}
		dnsServers = append(dnsServers, map[string]any{
			"type":       "hosts",
			"tag":        "hosts",
			"predefined": predefined,
		})
		dnsRules = append(dnsRules, map[string]any{
			"domain": domains,
			"server": "hosts",
		})
	}
	if network.DNSServer != "" {
		dnsIP, parseErr := netip.ParseAddr(network.DNSServer)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid cluster DNS address %q: %w", network.DNSServer, parseErr)
		}
		dnsServers = append(dnsServers, map[string]any{
			"type":   "udp",
			"tag":    "cluster",
			"server": dnsIP.String(),
			"detour": KubernetesOutbound,
		})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": clusterDomains,
			"server":        "cluster",
		})
	}
	dnsServers = append(dnsServers, map[string]any{"type": "local", "tag": "local"})

	trafficIn := []string{TrafficInbound}
	clusterUsers := clusterTrafficUsers()
	localUsers := localTrafficUsers()
	routeRules := []map[string]any{
		{"inbound": []string{"dns-in"}, "action": "hijack-dns"},
		{
			"inbound":   trafficIn,
			"auth_user": clusterUsers,
			"outbound":  KubernetesOutbound,
		},
	}
	for _, route := range routes {
		routeRules = append(routeRules, map[string]any{
			"inbound":   trafficIn,
			"auth_user": localUsers,
			"ip_cidr":   []string{route},
			"action":    "reject",
		})
	}
	routeRules = append(routeRules,
		map[string]any{
			"inbound":   trafficIn,
			"auth_user": localUsers,
			"ip_cidr":   []string{"127.0.0.0/8", "::1/128"},
			"outbound":  LocalOutbound,
		},
		map[string]any{
			"inbound":   trafficIn,
			"auth_user": localUsers,
			"domain":    []string{"localhost"},
			"outbound":  LocalOutbound,
		},
		map[string]any{
			"inbound":       trafficIn,
			"auth_user":     localUsers,
			"ip_is_private": true,
			"outbound":      LocalOutbound,
		},
		map[string]any{
			"inbound":   trafficIn,
			"auth_user": localUsers,
			"action":    "reject",
		},
		// Unknown or missing auth_user on traffic-in must not fall through to
		// TUN/cluster rules (UDP ASSOCIATE dye loss would otherwise misroute).
		map[string]any{"inbound": trafficIn, "action": "reject"},
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{
			"domain_suffix": clusterDomains, "outbound": KubernetesOutbound,
		},
	)
	for _, route := range routes {
		routeRules = append(routeRules, map[string]any{
			"ip_cidr":  []string{route},
			"outbound": KubernetesOutbound,
		})
	}

	tunInbound := map[string]any{
		// dns_mode is sing-box 1.14+; we pin 1.13 and use /etc/resolver
		// (or platform split DNS) + dns-in instead of TUN DNS hijack.
		"type":       "tun",
		"tag":        "tun-in",
		"address":    []string{options.TUNAddress},
		"mtu":        9000,
		"auto_route": true,
		// Windows WFP strict_route blocks DNS on other interfaces
		"strict_route": runtime.GOOS != "windows",
		// Linux: auto_redirect uses nftables and avoids TUN vs Docker/Minikube
		// bridge conflicts that otherwise break kubectl port-forward to Gateway.
		// See https://sing-box.sagernet.org/configuration/inbound/tun/
		"auto_redirect": runtime.GOOS == "linux",
		"stack":         "mixed",
		"route_address": routes,
	}
	inbounds := []map[string]any{
		tunInbound,
		{
			"type":        "direct",
			"tag":         "dns-in",
			"listen":      options.DNSHost,
			"listen_port": options.DNSPort,
		},
	}
	if options.TrafficPorts.Listen != 0 {
		if options.TrafficPassword == "" {
			return nil, errors.New("traffic inbound password is required")
		}
		if err := validatePort(options.TrafficPorts.Listen, TrafficInbound); err != nil {
			return nil, err
		}
		users := make([]map[string]any, 0, len(TrafficFeatureUsers()))
		for _, username := range TrafficFeatureUsers() {
			users = append(users, map[string]any{
				"username": username,
				"password": options.TrafficPassword,
			})
		}
		inbounds = append(inbounds, map[string]any{
			"type":        "socks",
			"tag":         TrafficInbound,
			"listen":      "127.0.0.1",
			"listen_port": options.TrafficPorts.Listen,
			"users":       users,
		})
	}

	config := map[string]any{
		"log": map[string]any{"level": "info", "output": "sing-box.log"},
		"dns": map[string]any{
			"servers":  dnsServers,
			"rules":    dnsRules,
			"final":    "local",
			"strategy": "prefer_ipv4",
		},
		"inbounds": inbounds,
		"outbounds": []map[string]any{
			{
				"type":        "socks",
				"tag":         KubernetesOutbound,
				"server":      options.BridgeHost,
				"server_port": options.BridgePort,
				"version":     "5",
			},
			{"type": "direct", "tag": LocalOutbound},
			{"type": "direct", "tag": DirectOutbound},
		},
		"route": map[string]any{
			"rules":                   routeRules,
			"final":                   DirectOutbound,
			"auto_detect_interface":   true,
			"find_process":            true,
			"default_domain_resolver": "local",
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

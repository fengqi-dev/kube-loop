package singbox

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
)

type generatedDNS struct {
	servers        []map[string]any
	rules          []map[string]any
	clusterDomains []string
}

func normalizeOptions(options Options) (Options, error) {
	if options.BridgeHost == "" {
		options.BridgeHost = DefaultDNSListen
	}
	if options.ControllerHost == "" {
		options.ControllerHost = DefaultDNSListen
	}
	if options.DNSHost == "" {
		options.DNSHost = DefaultDNSListen
	}
	if options.DNSPort == 0 {
		options.DNSPort = DefaultDNSPort
	}
	if err := validatePort(options.BridgePort, "bridge"); err != nil {
		return Options{}, err
	}
	if err := validatePort(options.ControllerPort, "controller"); err != nil {
		return Options{}, err
	}
	if err := validatePort(options.DNSPort, "dns"); err != nil {
		return Options{}, err
	}
	if options.ControllerSecret == "" {
		return Options{}, errors.New("controller secret is required")
	}
	if options.TUNAddress == "" {
		options.TUNAddress = defaultTUNAddress
	}
	if _, err := netip.ParsePrefix(options.TUNAddress); err != nil {
		return Options{}, fmt.Errorf("invalid TUN address %q: %w", options.TUNAddress, err)
	}
	return options, nil
}

func buildDNSConfig(network NetworkSpec, options Options) (generatedDNS, error) {
	hosts, err := NormalizeHostAliases(options.Hosts)
	if err != nil {
		return generatedDNS{}, err
	}

	clusterDomains, err := dns.NormalizeClusterDomains(options.ClusterDomains)
	if err != nil {
		return generatedDNS{}, err
	}
	if len(network.ClusterDomains) > 0 {
		allDomains := append(append([]string{}, clusterDomains...), network.ClusterDomains...)
		merged, mergeErr := dns.NormalizeClusterDomains(allDomains)
		if mergeErr != nil {
			return generatedDNS{}, mergeErr
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
			configTypeKey: "hosts",
			configTagKey:  hostsDNSServer,
			"predefined":  predefined,
		})
		dnsRules = append(dnsRules, map[string]any{
			"domain":        domains,
			configServerKey: hostsDNSServer,
		})
	}
	if network.DNSServer != "" {
		dnsIP, parseErr := netip.ParseAddr(network.DNSServer)
		if parseErr != nil {
			return generatedDNS{}, fmt.Errorf("invalid cluster DNS address %q: %w", network.DNSServer, parseErr)
		}
		dnsServers = append(dnsServers, map[string]any{
			configTypeKey:   "udp",
			configTagKey:    "cluster",
			configServerKey: dnsIP.String(),
			"detour":        KubernetesOutbound,
		})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": clusterDomains,
			configServerKey: "cluster",
		})
	}
	dnsServers = append(dnsServers, map[string]any{
		configTypeKey: LocalOutbound,
		configTagKey:  LocalOutbound,
	})
	return generatedDNS{
		servers: dnsServers, rules: dnsRules, clusterDomains: clusterDomains,
	}, nil
}

func buildRouteRules(routes, clusterDomains []string) []map[string]any {
	trafficIn := []string{TrafficInbound}
	localUsers := localTrafficUsers()
	routeRules := []map[string]any{
		{configInboundKey: []string{"dns-in"}, configActionKey: "hijack-dns"},
		{
			configInboundKey:  trafficIn,
			configAuthUserKey: localUsers,
			configIPCIDRKey:   routes,
			configActionKey:   rejectRouteAction,
		},
	}
	routeRules = append(routeRules,
		map[string]any{
			configInboundKey:  trafficIn,
			configAuthUserKey: localUsers,
			configIPCIDRKey:   []string{"127.0.0.0/8", "::1/128"},
			configOutboundKey: LocalOutbound,
		},
		map[string]any{
			configInboundKey:  trafficIn,
			configAuthUserKey: localUsers,
			"domain":          []string{"localhost"},
			configOutboundKey: LocalOutbound,
		},
		map[string]any{
			configInboundKey:  trafficIn,
			configAuthUserKey: localUsers,
			"ip_is_private":   true,
			configOutboundKey: LocalOutbound,
		},
		// Unknown or missing auth_user on traffic-in must not fall through to
		// TUN/cluster rules (UDP ASSOCIATE dye loss would otherwise misroute).
		map[string]any{configInboundKey: trafficIn, configActionKey: rejectRouteAction},
		map[string]any{configActionKey: "sniff"},
		map[string]any{"protocol": "dns", configActionKey: "hijack-dns"},
		// All UDP from the TUN is carried through the Kubernetes SOCKS
		// outbound; socksbridge performs UDP ASSOCIATE over the gateway.
		map[string]any{
			"network": []string{"udp"}, configOutboundKey: KubernetesOutbound,
		},
		map[string]any{
			"domain_suffix": clusterDomains, configOutboundKey: KubernetesOutbound,
		},
		map[string]any{
			configIPCIDRKey:   routes,
			configOutboundKey: KubernetesOutbound,
		},
	)
	return routeRules
}

func buildInbounds(routes []string, options Options) ([]map[string]any, error) {
	tunInbound := map[string]any{
		// dns_mode is sing-box 1.14+; we pin 1.13 and use /etc/resolver
		// (or platform split DNS) + dns-in instead of TUN DNS hijack.
		configTypeKey: "tun",
		configTagKey:  "tun-in",
		"address":     []string{options.TUNAddress},
		"mtu":         9000,
		"auto_route":  true,
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
			configTypeKey: DirectOutbound,
			configTagKey:  "dns-in",
			"listen":      options.DNSHost,
			"listen_port": options.DNSPort,
		},
	}
	if options.TrafficPorts.Listen == 0 {
		return inbounds, nil
	}
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
		configTypeKey: "socks",
		configTagKey:  TrafficInbound,
		"listen":      DefaultDNSListen,
		"listen_port": options.TrafficPorts.Listen,
		"users":       users,
	})
	return inbounds, nil
}

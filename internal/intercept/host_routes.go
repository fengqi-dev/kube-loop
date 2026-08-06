package intercept

import (
	"fmt"
	"strings"
)

type hostRouteKey struct {
	host string
	port uint16
}

// hostRoute serves host TUN traffic to an intercepted Service locally, so the
// desktop does not depend on kube-proxy hairpin through the Gateway.
type hostRoute struct {
	mode        string
	preview     bool
	local       PortMapping
	primaryAddr string
}

// hostRouteRegistry owns the host and Service DNS indexes used by the local
// SOCKS bridge. Manager.mu protects every call so route and runtime lifecycle
// changes can be published atomically.
type hostRouteRegistry struct {
	byTarget map[hostRouteKey]hostRoute
}

func newHostRouteRegistry() *hostRouteRegistry {
	return &hostRouteRegistry{byTarget: make(map[hostRouteKey]hostRoute)}
}

func (r *hostRouteRegistry) install(
	service *Service,
	ports []InterceptPort,
	portKeys map[string]PortMapping,
	primaryAddrs map[string]string,
	mode string,
	preview bool,
	interceptID string,
) []hostRouteKey {
	hosts := serviceRewriteHosts(service)
	if len(hosts) == 0 {
		return nil
	}
	if mode == "" {
		mode = ModeExchange
	}
	keys := make([]hostRouteKey, 0, len(hosts)*len(ports))
	for _, port := range ports {
		network := protocolToNetwork(port.Protocol)
		subID := fmt.Sprintf("%s:%s:%d", interceptID, networkName(network), port.ServicePort)
		local := localFor(port, nil)
		if mapped, ok := portKeys[subID]; ok {
			local = mapped
		}
		primary := ""
		if primaryAddrs != nil {
			primary = primaryAddrs[subID]
		}
		for _, host := range hosts {
			key := hostRouteKey{host: host, port: uint16(port.ServicePort)}
			r.byTarget[key] = hostRoute{
				mode: mode, preview: preview, local: local, primaryAddr: primary,
			}
			keys = append(keys, key)
		}
	}
	return keys
}

func (r *hostRouteRegistry) remove(keys []hostRouteKey) {
	for _, key := range keys {
		delete(r.byTarget, key)
	}
}

func (r *hostRouteRegistry) lookup(host string, port uint16) (hostRoute, bool) {
	route, ok := r.byTarget[hostRouteKey{
		host: normalizeRouteHost(host),
		port: port,
	}]
	return route, ok
}

func serviceRewriteHosts(service *Service) []string {
	if service == nil {
		return nil
	}
	hosts := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(host string) {
		host = normalizeRouteHost(host)
		if host == "" || host == "none" {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	add(service.ClusterIP)
	name := service.Name
	namespace := service.Namespace
	if name != "" && namespace != "" {
		add(name + "." + namespace + ".svc.cluster.local")
		add(name + "." + namespace + ".svc")
		add(name + "." + namespace)
	}
	return hosts
}

func normalizeRouteHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

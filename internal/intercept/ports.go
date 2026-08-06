package intercept

import (
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

func normalizePreviewPorts(ports []PortMapping) ([]PortMapping, error) {
	if len(ports) == 0 {
		return nil, fmt.Errorf("at least one port mapping is required")
	}
	locals := make([]PortMapping, len(ports))
	seen := make(map[string]struct{}, len(ports))
	for i, port := range ports {
		if port.ServicePort <= 0 || port.ServicePort > 65535 {
			return nil, fmt.Errorf("invalid service port %d", port.ServicePort)
		}
		if port.LocalPort <= 0 || port.LocalPort > 65535 {
			return nil, fmt.Errorf("invalid local port %d", port.LocalPort)
		}
		if port.LocalHost == "" {
			port.LocalHost = "127.0.0.1"
		}
		if port.Protocol == "" {
			port.Protocol = "TCP"
		}
		key := fmt.Sprintf("%s:%d", normalizeProtocol(port.Protocol), port.ServicePort)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate service port %s", key)
		}
		seen[key] = struct{}{}
		locals[i] = port
	}
	return locals, nil
}

func buildPreviewPorts(
	locals []PortMapping, allocate func() int32,
) []InterceptPort {
	ports := make([]InterceptPort, 0, len(locals))
	for _, local := range locals {
		protocol := ProtocolTCP
		if normalizeProtocol(local.Protocol) == "UDP" {
			protocol = ProtocolUDP
		}
		name := fmt.Sprintf("%s-%d", strings.ToLower(protocol), local.ServicePort)
		ports = append(ports, InterceptPort{
			Name:        name,
			Protocol:    protocol,
			ServicePort: local.ServicePort,
			ListenPort:  allocate(),
		})
	}
	return ports
}

func buildPortsForLocals(
	service *Service,
	locals []PortMapping,
	allocate func() int32,
) ([]InterceptPort, error) {
	ports := make([]InterceptPort, 0, len(locals))
	for _, local := range locals {
		found := false
		for _, servicePort := range service.Ports {
			protocol := servicePort.Protocol
			if protocol == "" {
				protocol = ProtocolTCP
			}
			if servicePort.Port != local.ServicePort || !equalProtocol(local.Protocol, protocol) {
				continue
			}
			name := servicePort.Name
			if name == "" {
				name = networkName(protocolToNetwork(protocol)) + fmt.Sprintf("-%d", servicePort.Port)
			}
			ports = append(ports, InterceptPort{
				Name:        name,
				Protocol:    protocol,
				ServicePort: servicePort.Port,
				ListenPort:  allocate(),
			})
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf(
				"service port %d/%s not found on %s/%s",
				local.ServicePort, local.Protocol, service.Namespace, service.Name,
			)
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("no ports to intercept")
	}
	return ports, nil
}

func protocolToNetwork(protocol string) byte {
	if normalizeProtocol(protocol) == ProtocolUDP {
		return tunnel.NetworkUDP
	}
	return tunnel.NetworkTCP
}

func (m Mapping) resolveLocals(service *Service) ([]PortMapping, error) {
	if len(m.Ports) == 0 {
		locals := make([]PortMapping, 0, len(service.Ports))
		for _, port := range service.Ports {
			protocol := port.Protocol
			if protocol == "" {
				protocol = "TCP"
			}
			locals = append(locals, PortMapping{
				ServicePort: port.Port,
				Protocol:    protocol,
				LocalHost:   "127.0.0.1",
				LocalPort:   int(port.Port),
			})
		}
		if len(locals) == 0 {
			return nil, fmt.Errorf("service has no ports")
		}
		return locals, nil
	}
	for i := range m.Ports {
		if m.Ports[i].LocalHost == "" {
			m.Ports[i].LocalHost = "127.0.0.1"
		}
		if m.Ports[i].LocalPort == 0 {
			m.Ports[i].LocalPort = int(m.Ports[i].ServicePort)
		}
		if m.Ports[i].Protocol == "" {
			m.Ports[i].Protocol = "TCP"
		}
	}
	return m.Ports, nil
}

func localFor(port InterceptPort, locals []PortMapping) PortMapping {
	for _, local := range locals {
		if local.ServicePort == port.ServicePort && equalProtocol(local.Protocol, port.Protocol) {
			return local
		}
	}
	return PortMapping{
		ServicePort: port.ServicePort,
		Protocol:    port.Protocol,
		LocalHost:   "127.0.0.1",
		LocalPort:   int(port.ServicePort),
	}
}

func equalProtocol(a, b string) bool {
	if a == "" {
		a = "TCP"
	}
	if b == "" {
		b = "TCP"
	}
	return normalizeProtocol(a) == normalizeProtocol(b)
}

func normalizeProtocol(value string) string {
	switch value {
	case "udp", "UDP", "Udp":
		return "UDP"
	default:
		return "TCP"
	}
}

func networkName(network byte) string {
	if network == tunnel.NetworkUDP {
		return "udp"
	}
	return "tcp"
}

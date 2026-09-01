package mirror

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type LocalTarget struct {
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
	LocalHost   string `json:"localHost"`
	LocalPort   uint16 `json:"localPort"`
}

func normalizeTargets(input []LocalTarget) ([]LocalTarget, []remote.MirrorPort, error) {
	if len(input) == 0 || len(input) > 64 {
		return nil, nil, errors.New("mirror requires one to 64 local targets")
	}
	targets := make([]LocalTarget, len(input))
	ports := make([]remote.MirrorPort, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, target := range input {
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		if target.Protocol == "" {
			target.Protocol = mirrorProtocolTCP
		}
		target.LocalHost = strings.TrimSpace(target.LocalHost)
		if target.LocalHost == "" {
			target.LocalHost = mirrorLoopbackHost
		}
		if target.LocalPort == 0 && target.ServicePort > 0 && target.ServicePort <= 65535 {
			target.LocalPort = uint16(target.ServicePort)
		}
		if target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort == 0 ||
			(target.Protocol != mirrorProtocolTCP && target.Protocol != mirrorProtocolUDP) || !validLocalHost(target.LocalHost) {
			return nil, nil, errors.New("mirror local target is invalid")
		}
		key := targetKey(target.Protocol, target.ServicePort)
		if _, exists := seen[key]; exists {
			return nil, nil, errors.New("mirror Service ports must be unique")
		}
		seen[key] = struct{}{}
		targets[index] = target
		ports[index] = remote.MirrorPort{ServicePort: target.ServicePort, Protocol: target.Protocol}
	}
	return targets, ports, nil
}

func remoteTargets(targets []LocalTarget) []remote.LocalTarget {
	items := make([]remote.LocalTarget, len(targets))
	for index, target := range targets {
		items[index] = remote.LocalTarget{
			Protocol: target.Protocol, ServicePort: target.ServicePort,
			LocalHost: target.LocalHost, LocalPort: target.LocalPort,
		}
	}
	return items
}

func validLocalHost(host string) bool {
	if address := net.ParseIP(host); address != nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	if len(host) > 253 || strings.ContainsAny(host, " /\\\t\r\n") {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func matchTaskTargets(task remote.MirrorTask, targets []LocalTarget) error {
	if len(task.Ports) != len(targets) {
		return errors.New("gateway changed the requested Mirror ports")
	}
	want := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		want[targetKey(target.Protocol, target.ServicePort)] = struct{}{}
	}
	for _, port := range task.Ports {
		key := targetKey(port.Protocol, port.ServicePort)
		if _, exists := want[key]; !exists {
			return errors.New("gateway changed the requested Mirror ports")
		}
		delete(want, key)
	}
	return nil
}

func targetKey(protocol string, port int32) string {
	return strings.ToLower(protocol) + "/" + strconv.Itoa(int(port))
}

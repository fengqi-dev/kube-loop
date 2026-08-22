package exchange

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
)

type LocalTarget = reverserelay.Target

func normalizeTargets(input []LocalTarget) ([]LocalTarget, []remote.ExchangePort, error) {
	if len(input) == 0 || len(input) > 64 {
		return nil, nil, errors.New("exchange requires one to 64 local targets")
	}
	targets := make([]LocalTarget, len(input))
	ports := make([]remote.ExchangePort, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, target := range input {
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		if target.Protocol == "" {
			target.Protocol = exchangeProtocolTCP
		}
		target.LocalHost = strings.TrimSpace(target.LocalHost)
		if target.LocalHost == "" {
			target.LocalHost = exchangeLoopbackHost
		}
		if target.LocalPort == 0 && target.ServicePort > 0 && target.ServicePort <= 65535 {
			target.LocalPort = uint16(target.ServicePort)
		}
		invalidPort := target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort == 0
		invalidProtocol := target.Protocol != exchangeProtocolTCP && target.Protocol != exchangeProtocolUDP
		if invalidPort || invalidProtocol || !validLocalHost(target.LocalHost) {
			return nil, nil, errors.New("exchange local target is invalid")
		}
		key := targetKey(target.Protocol, target.ServicePort)
		if _, exists := seen[key]; exists {
			return nil, nil, errors.New("exchange Service ports must be unique")
		}
		seen[key] = struct{}{}
		targets[index] = target
		ports[index] = remote.ExchangePort{ServicePort: target.ServicePort, Protocol: target.Protocol}
	}
	return targets, ports, nil
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

func matchTaskTargets(task remote.ExchangeTask, targets []LocalTarget) error {
	if len(task.Ports) != len(targets) {
		return errors.New("gateway changed the requested Exchange ports")
	}
	want := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		want[targetKey(target.Protocol, target.ServicePort)] = struct{}{}
	}
	for _, port := range task.Ports {
		key := targetKey(port.Protocol, port.ServicePort)
		if _, exists := want[key]; !exists {
			return errors.New("gateway changed the requested Exchange ports")
		}
		delete(want, key)
	}
	return nil
}

func targetKey(protocol string, port int32) string {
	return strings.ToLower(protocol) + "/" + strconv.Itoa(int(port))
}

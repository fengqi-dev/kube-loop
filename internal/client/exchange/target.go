package exchange

import (
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/utils"
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
		if invalidPort || invalidProtocol || !utils.ValidLocalHost(target.LocalHost) {
			return nil, nil, errors.New("exchange local target is invalid")
		}
		key := utils.TargetKey(target.Protocol, target.ServicePort)
		if _, exists := seen[key]; exists {
			return nil, nil, errors.New("exchange Service ports must be unique")
		}
		seen[key] = struct{}{}
		targets[index] = target
		ports[index] = remote.ExchangePort{ServicePort: target.ServicePort, Protocol: target.Protocol}
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

func matchTaskTargets(task remote.ExchangeTask, targets []LocalTarget) error {
	if len(task.Ports) != len(targets) {
		return errors.New("gateway changed the requested Exchange ports")
	}
	want := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		want[utils.TargetKey(target.Protocol, target.ServicePort)] = struct{}{}
	}
	for _, port := range task.Ports {
		key := utils.TargetKey(port.Protocol, port.ServicePort)
		if _, exists := want[key]; !exists {
			return errors.New("gateway changed the requested Exchange ports")
		}
		delete(want, key)
	}
	return nil
}

func exchangeTargets(items []remote.LocalTarget) []LocalTarget {
	targets := make([]LocalTarget, len(items))
	for index, item := range items {
		targets[index] = LocalTarget{
			Protocol: item.Protocol, ServicePort: item.ServicePort,
			LocalHost: item.LocalHost, LocalPort: item.LocalPort,
		}
	}
	return targets
}

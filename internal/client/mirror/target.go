package mirror

import (
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type LocalTarget = reverserelay.Target

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
			(target.Protocol != mirrorProtocolTCP && target.Protocol != mirrorProtocolUDP) || !utils.ValidLocalHost(target.LocalHost) {
			return nil, nil, errors.New("mirror local target is invalid")
		}
		key := utils.TargetKey(target.Protocol, target.ServicePort)
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

func matchTaskTargets(task remote.MirrorTask, targets []LocalTarget) error {
	if len(task.Ports) != len(targets) {
		return errors.New("gateway changed the requested Mirror ports")
	}
	want := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		want[utils.TargetKey(target.Protocol, target.ServicePort)] = struct{}{}
	}
	for _, port := range task.Ports {
		key := utils.TargetKey(port.Protocol, port.ServicePort)
		if _, exists := want[key]; !exists {
			return errors.New("gateway changed the requested Mirror ports")
		}
		delete(want, key)
	}
	return nil
}

func mirrorTargets(items []remote.LocalTarget) []LocalTarget {
	targets := make([]LocalTarget, len(items))
	for index, item := range items {
		targets[index] = LocalTarget{
			Protocol: item.Protocol, ServicePort: item.ServicePort,
			LocalHost: item.LocalHost, LocalPort: item.LocalPort,
		}
	}
	return targets
}

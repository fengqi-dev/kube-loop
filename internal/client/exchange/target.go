package exchange

import (
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type LocalTarget = reverserelay.Target

func normalizeTargets(input []LocalTarget) ([]LocalTarget, []remote.ExchangePort, error) {
	targets, err := reverserelay.NormalizeTargets(input, "exchange")
	if err != nil {
		return nil, nil, err
	}
	ports := make([]remote.ExchangePort, len(targets))
	for index, target := range targets {
		ports[index] = remote.ExchangePort{ServicePort: target.ServicePort, Protocol: target.Protocol}
	}
	return targets, ports, nil
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

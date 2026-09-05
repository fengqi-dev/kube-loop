package mirror

import (
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type LocalTarget = reverserelay.Target

func normalizeTargets(input []LocalTarget) ([]LocalTarget, []remote.MirrorPort, error) {
	targets, err := reverserelay.NormalizeTargets(input, "mirror")
	if err != nil {
		return nil, nil, err
	}
	ports := make([]remote.MirrorPort, len(targets))
	for index, target := range targets {
		ports[index] = remote.MirrorPort{ServicePort: target.ServicePort, Protocol: target.Protocol}
	}
	return targets, ports, nil
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

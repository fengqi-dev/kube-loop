package preview

import (
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type LocalTarget = reverserelay.Target

func normalizeTargets(input []LocalTarget) ([]LocalTarget, []remote.PreviewPort, error) {
	targets, err := reverserelay.NormalizeTargets(input, "preview")
	if err != nil {
		return nil, nil, err
	}
	ports := make([]remote.PreviewPort, len(targets))
	for index, target := range targets {
		ports[index] = remote.PreviewPort{ServicePort: target.ServicePort, Protocol: target.Protocol}
	}
	return targets, ports, nil
}

func matchTask(task remote.PreviewTask, name string, targets []LocalTarget) error {
	if task.Name != name || len(task.Ports) != len(targets) {
		return errors.New("gateway changed the requested Preview identity")
	}
	want := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		want[utils.TargetKey(target.Protocol, target.ServicePort)] = struct{}{}
	}
	for _, port := range task.Ports {
		key := utils.TargetKey(port.Protocol, port.ServicePort)
		if _, exists := want[key]; !exists {
			return errors.New("gateway changed the requested Preview ports")
		}
		delete(want, key)
	}
	return nil
}

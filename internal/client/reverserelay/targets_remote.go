package reverserelay

import "github.com/fengqi-dev/kube-loop/internal/client/remote"

// RemoteTargets copies local destinations into the remote API representation.
func RemoteTargets(targets []Target) []remote.LocalTarget {
	items := make([]remote.LocalTarget, len(targets))
	for index, target := range targets {
		items[index] = remote.LocalTarget{
			Protocol: target.Protocol, ServicePort: target.ServicePort,
			LocalHost: target.LocalHost, LocalPort: target.LocalPort,
		}
	}
	return items
}

// LocalTargets copies persisted destinations into the local relay representation.
func LocalTargets(items []remote.LocalTarget) []Target {
	targets := make([]Target, len(items))
	for index, item := range items {
		targets[index] = Target{
			Protocol: item.Protocol, ServicePort: item.ServicePort,
			LocalHost: item.LocalHost, LocalPort: item.LocalPort,
		}
	}
	return targets
}

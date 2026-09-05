package reverserelay

import (
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

// NormalizeTargets returns a normalized copy of the local destinations.
// Operation identifies the traffic task in validation errors.
func NormalizeTargets(input []Target, operation string) ([]Target, error) {
	if len(input) == 0 || len(input) > 64 {
		return nil, errors.New(operation + " requires one to 64 local targets")
	}
	targets := make([]Target, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, target := range input {
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		if target.Protocol == "" {
			target.Protocol = "tcp"
		}
		target.LocalHost = strings.TrimSpace(target.LocalHost)
		if target.LocalHost == "" {
			target.LocalHost = "127.0.0.1"
		}
		if target.LocalPort == 0 && target.ServicePort > 0 && target.ServicePort <= 65535 {
			target.LocalPort = uint16(target.ServicePort)
		}
		invalidPort := target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort == 0
		invalidProtocol := target.Protocol != "tcp" && target.Protocol != "udp"
		if invalidPort || invalidProtocol || !utils.ValidLocalHost(target.LocalHost) {
			return nil, errors.New(operation + " local target is invalid")
		}
		key := utils.TargetKey(target.Protocol, target.ServicePort)
		if _, exists := seen[key]; exists {
			return nil, errors.New(operation + " Service ports must be unique")
		}
		seen[key] = struct{}{}
		targets[index] = target
	}
	return targets, nil
}

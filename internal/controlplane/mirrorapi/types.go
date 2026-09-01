package mirrorapi

import "github.com/fengqi-dev/kube-loop/internal/controlplane/entity"

type Spec struct {
	Service      string               `json:"service"`
	Ports        []entity.Port        `json:"ports"`
	LocalTargets []entity.LocalTarget `json:"localTargets,omitempty"`
}

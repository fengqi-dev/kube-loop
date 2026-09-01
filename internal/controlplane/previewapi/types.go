package previewapi

import "github.com/fengqi-dev/kube-loop/internal/controlplane/entity"

type Spec struct {
	Name         string               `json:"name"`
	Ports        []entity.Port        `json:"ports"`
	LocalTargets []entity.LocalTarget `json:"localTargets,omitempty"`
}

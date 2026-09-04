package previewapi

import "github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"

type Spec struct {
	Name         string                     `json:"name"`
	Ports        []servicemodel.Port        `json:"ports"`
	LocalTargets []servicemodel.LocalTarget `json:"localTargets,omitempty"`
}
